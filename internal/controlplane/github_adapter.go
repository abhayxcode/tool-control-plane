package controlplane

import (
	"errors"
	"fmt"
	"net/http"
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
	case "code_host.get_recent_changes",
		"code_host.create_draft_pr",
		"ci.get_checks",
		"ci.get_logs":
		return nil, fmt.Errorf("github adapter live execution is not implemented for '%s' yet", definition.ID)
	default:
		return nil, fmt.Errorf("github adapter does not support capability '%s'", definition.ID)
	}
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
