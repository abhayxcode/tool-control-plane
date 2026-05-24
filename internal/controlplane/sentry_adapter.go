package controlplane

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const SentryProvider = "sentry"

type SentryAdapterConfig struct {
	Token   string
	Org     string
	Project string
	BaseURL string
	Client  *http.Client
}

type SentryAdapter struct {
	token   string
	org     string
	project string
	baseURL string
	client  *http.Client
}

func NewSentryAdapter(config SentryAdapterConfig) SentryAdapter {
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if baseURL == "" {
		baseURL = "https://sentry.io"
	}
	client := config.Client
	if client == nil {
		client = http.DefaultClient
	}
	return SentryAdapter{
		token:   strings.TrimSpace(config.Token),
		org:     strings.TrimSpace(config.Org),
		project: strings.TrimSpace(config.Project),
		baseURL: baseURL,
		client:  client,
	}
}

func (a SentryAdapter) Execute(definition CapabilityDefinition, req ToolCallRequest) (map[string]any, error) {
	if strings.TrimSpace(a.token) == "" {
		return nil, errors.New("sentry adapter requires SENTRY_AUTH_TOKEN")
	}
	if definition.ID != "errors.get_recent_errors" {
		return nil, fmt.Errorf("sentry adapter does not support capability '%s'", definition.ID)
	}
	return a.getRecentErrors(req)
}

func (a SentryAdapter) getRecentErrors(req ToolCallRequest) (map[string]any, error) {
	org := firstNonEmpty(firstStringArg(req.Arguments, "organization", "organization_slug", "org"), a.org)
	project := firstNonEmpty(firstStringArg(req.Arguments, "project", "project_slug"), a.project)
	if org == "" || project == "" {
		return nil, errors.New("sentry errors.get_recent_errors requires organization and project arguments or SENTRY_ORG and SENTRY_PROJECT")
	}
	limit := optionalIntArg(req.Arguments, "limit", 5)
	if limit <= 0 {
		limit = 5
	}
	if limit > 20 {
		limit = 20
	}
	query := firstStringArg(req.Arguments, "query")
	if query == "" {
		query = "is:unresolved"
	}
	if environment := firstStringArg(req.Arguments, "environment", "env"); environment != "" && !strings.Contains(query, "environment:") {
		query = strings.TrimSpace(query + " environment:" + environment)
	}
	statsPeriod := firstNonEmpty(firstStringArg(req.Arguments, "stats_period", "statsPeriod"), "24h")
	sort := firstNonEmpty(firstStringArg(req.Arguments, "sort"), "freq")

	values := url.Values{}
	values.Set("query", query)
	values.Set("statsPeriod", statsPeriod)
	values.Set("sort", sort)
	values.Set("limit", strconv.Itoa(limit))
	requestURL := fmt.Sprintf("%s/api/0/projects/%s/%s/issues/?%s", a.baseURL, url.PathEscape(org), url.PathEscape(project), values.Encode())
	httpReq, err := http.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+a.token)
	httpReq.Header.Set("Accept", "application/json")
	resp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("sentry issues request failed: %w", err)
	}
	defer resp.Body.Close()
	var issues []sentryIssue
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("sentry issues request returned HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&issues); err != nil {
		return nil, err
	}
	topErrors := make([]map[string]any, 0, len(issues))
	for _, issue := range issues {
		topErrors = append(topErrors, map[string]any{
			"id":         issue.ID,
			"short_id":   issue.ShortID,
			"title":      firstNonEmpty(issue.Title, issue.Metadata.Title),
			"culprit":    issue.Culprit,
			"level":      issue.Level,
			"status":     issue.Status,
			"count":      issue.Count,
			"user_count": issue.UserCount,
			"first_seen": issue.FirstSeen,
			"last_seen":  issue.LastSeen,
			"url":        issue.Permalink,
			"source_url": issue.Permalink,
		})
	}
	sourceURL := ""
	if len(issues) > 0 {
		sourceURL = issues[0].Permalink
	}
	status := "healthy"
	if len(issues) > 0 {
		status = "degraded"
	}
	return map[string]any{
		"status":     status,
		"top_errors": topErrors,
		"query":      query,
		"source_url": sourceURL,
		"evidence":   sentryEvidence(org, project, issues),
	}, nil
}

type sentryIssue struct {
	ID        string `json:"id"`
	ShortID   string `json:"shortId"`
	Title     string `json:"title"`
	Culprit   string `json:"culprit"`
	Level     string `json:"level"`
	Status    string `json:"status"`
	Count     string `json:"count"`
	UserCount int    `json:"userCount"`
	FirstSeen string `json:"firstSeen"`
	LastSeen  string `json:"lastSeen"`
	Permalink string `json:"permalink"`
	Metadata  struct {
		Title string `json:"title"`
	} `json:"metadata"`
}

func sentryEvidence(org string, project string, issues []sentryIssue) string {
	if len(issues) == 0 {
		return fmt.Sprintf("Sentry returned no unresolved issues for %s/%s.", org, project)
	}
	top := issues[0]
	title := firstNonEmpty(top.Title, top.Metadata.Title, top.ShortID, top.ID)
	return fmt.Sprintf("Sentry returned %d issue(s) for %s/%s; top issue %s occurred %s time(s).", len(issues), org, project, title, top.Count)
}

func SentryProviderOverrides() map[string]string {
	return map[string]string{
		"errors.get_recent_errors": SentryProvider,
	}
}
