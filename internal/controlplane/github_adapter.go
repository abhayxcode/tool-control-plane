package controlplane

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const GitHubProvider = "github"

type GitHubAdapterConfig struct {
	Token   string
	BaseURL string
	Client  *http.Client
}

type GitHubAdapter struct {
	token   string
	baseURL string
	client  *http.Client
}

func NewGitHubAdapter(config GitHubAdapterConfig) GitHubAdapter {
	baseURL := strings.TrimRight(config.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	client := config.Client
	if client == nil {
		client = http.DefaultClient
	}
	return GitHubAdapter{
		token:   config.Token,
		baseURL: baseURL,
		client:  client,
	}
}

func (a GitHubAdapter) Execute(definition CapabilityDefinition, req ToolCallRequest) (map[string]any, error) {
	if strings.TrimSpace(a.token) == "" {
		return nil, errors.New("github adapter requires GITHUB_TOKEN")
	}

	switch definition.ID {
	case "ci.get_checks":
		return a.getChecks(req)
	case "code_host.get_recent_changes",
		"code_host.create_draft_pr",
		"ci.get_logs":
		return nil, fmt.Errorf("github adapter live execution is not implemented for '%s' yet", definition.ID)
	default:
		return nil, fmt.Errorf("github adapter does not support capability '%s'", definition.ID)
	}
}

func (a GitHubAdapter) getChecks(req ToolCallRequest) (map[string]any, error) {
	owner, repo, err := githubRepoArgs(req.Arguments)
	if err != nil {
		return nil, err
	}
	ref, err := githubRefArg(req.Arguments)
	if err != nil {
		prNumber, prErr := intArg(req.Arguments, "pr_number")
		if prErr != nil {
			return nil, err
		}
		ref, err = a.pullRequestHeadSHA(owner, repo, prNumber)
		if err != nil {
			return nil, err
		}
	}

	var response githubCheckRunsResponse
	path := fmt.Sprintf("/repos/%s/%s/commits/%s/check-runs", url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(ref))
	if err := a.getJSON(path, &response); err != nil {
		return nil, err
	}

	checks := make([]map[string]any, 0, len(response.CheckRuns))
	status := "passed"
	sourceURL := ""
	for _, run := range response.CheckRuns {
		check := map[string]any{
			"name":       run.Name,
			"status":     run.Status,
			"conclusion": run.Conclusion,
			"url":        run.HTMLURL,
		}
		checks = append(checks, check)
		if sourceURL == "" {
			sourceURL = run.HTMLURL
		}
		status = combineGitHubCheckStatus(status, run.Status, run.Conclusion)
	}
	if len(response.CheckRuns) == 0 {
		status = "pending"
	}

	evidence := fmt.Sprintf("GitHub returned %d check run(s) for %s/%s@%s.", len(response.CheckRuns), owner, repo, ref)
	return map[string]any{
		"status":     status,
		"commit_sha": ref,
		"checks":     checks,
		"evidence":   evidence,
		"source_url": sourceURL,
	}, nil
}

func (a GitHubAdapter) pullRequestHeadSHA(owner string, repo string, prNumber int) (string, error) {
	var response githubPullResponse
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d", url.PathEscape(owner), url.PathEscape(repo), prNumber)
	if err := a.getJSON(path, &response); err != nil {
		return "", err
	}
	if response.Head.SHA == "" {
		return "", fmt.Errorf("github pull request #%d did not include head SHA", prNumber)
	}
	return response.Head.SHA, nil
}

func (a GitHubAdapter) getJSON(path string, target any) error {
	requestURL := a.baseURL + path
	httpReq, err := http.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		return err
	}
	httpReq.Header.Set("Accept", "application/vnd.github+json")
	httpReq.Header.Set("Authorization", "Bearer "+a.token)
	httpReq.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	httpResp, err := a.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer httpResp.Body.Close()

	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return err
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode > 299 {
		return fmt.Errorf("github API request failed with status %d: %s", httpResp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.Unmarshal(body, target); err != nil {
		return err
	}
	return nil
}

func githubRepoArgs(args map[string]any) (string, string, error) {
	if repository, ok := stringArg(args, "repository"); ok {
		parts := strings.Split(repository, "/")
		if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			return parts[0], parts[1], nil
		}
		return "", "", fmt.Errorf("repository must use owner/repo format")
	}

	owner, ownerOK := stringArg(args, "owner")
	repo, repoOK := stringArg(args, "repo")
	if !repoOK {
		repo, repoOK = stringArg(args, "repository_name")
	}
	if !ownerOK || !repoOK {
		return "", "", fmt.Errorf("github ci.get_checks requires repository or owner and repo arguments")
	}
	return owner, repo, nil
}

func githubRefArg(args map[string]any) (string, error) {
	for _, key := range []string{"ref", "commit_sha", "sha", "head_sha"} {
		if value, ok := stringArg(args, key); ok {
			return value, nil
		}
	}
	return "", fmt.Errorf("github ci.get_checks requires ref, commit_sha, sha, head_sha, or pr_number argument")
}

func stringArg(args map[string]any, key string) (string, bool) {
	value, ok := args[key]
	if !ok {
		return "", false
	}
	text, ok := value.(string)
	if !ok {
		return "", false
	}
	text = strings.TrimSpace(text)
	return text, text != ""
}

func intArg(args map[string]any, key string) (int, error) {
	value, ok := args[key]
	if !ok {
		return 0, fmt.Errorf("missing %s argument", key)
	}
	switch typed := value.(type) {
	case int:
		return typed, nil
	case float64:
		return int(typed), nil
	case json.Number:
		result, err := typed.Int64()
		return int(result), err
	case string:
		result, err := strconv.Atoi(strings.TrimSpace(typed))
		if err != nil {
			return 0, fmt.Errorf("%s must be an integer", key)
		}
		return result, nil
	default:
		return 0, fmt.Errorf("%s must be an integer", key)
	}
}

func combineGitHubCheckStatus(current string, checkStatus string, conclusion string) string {
	failingConclusions := map[string]bool{
		"action_required": true,
		"cancelled":       true,
		"failure":         true,
		"startup_failure": true,
		"timed_out":       true,
	}
	if failingConclusions[conclusion] {
		return "failed"
	}
	if current == "failed" {
		return current
	}
	if checkStatus != "completed" || conclusion == "" {
		return "pending"
	}
	if current == "pending" {
		return current
	}
	return "passed"
}

type githubCheckRunsResponse struct {
	TotalCount int `json:"total_count"`
	CheckRuns  []struct {
		Name       string `json:"name"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
		HTMLURL    string `json:"html_url"`
	} `json:"check_runs"`
}

type githubPullResponse struct {
	Head struct {
		SHA string `json:"sha"`
	} `json:"head"`
}

func (a GitHubAdapter) BaseURL() string {
	return a.baseURL
}

func (a GitHubAdapter) HTTPClient() *http.Client {
	return a.client
}

func GitHubProviderOverrides() map[string]string {
	return map[string]string{
		"code_host.get_recent_changes": GitHubProvider,
		"code_host.create_draft_pr":    GitHubProvider,
		"ci.get_checks":                GitHubProvider,
		"ci.get_logs":                  GitHubProvider,
	}
}
