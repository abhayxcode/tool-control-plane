package controlplane

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAdapterRegistryExecutesProviderAdapter(t *testing.T) {
	registry := NewAdapterRegistry(map[string]ToolAdapter{
		"mock": NewMockAdapter(map[string]map[string]any{
			"metrics.get_service_health": {
				"status": "ok",
			},
		}),
	})

	response := registry.Execute(CapabilityDefinition{
		ID:         "metrics.get_service_health",
		RiskLevel:  RiskRead,
		Provider:   "mock",
		Capability: "metrics",
		Action:     "get_service_health",
	}, ToolCallRequest{
		ServiceID:   "backend",
		Environment: "prod",
	})

	if response.Status != "success" {
		t.Fatalf("expected success, got %q", response.Status)
	}
	if response.Provider != "mock" {
		t.Fatalf("expected mock provider, got %q", response.Provider)
	}
	if response.Result["status"] != "ok" {
		t.Fatalf("expected fixture result")
	}
}

func TestAdapterRegistryReturnsErrorForMissingProvider(t *testing.T) {
	registry := NewAdapterRegistry(map[string]ToolAdapter{})
	response := registry.Execute(CapabilityDefinition{
		ID:        "metrics.get_service_health",
		RiskLevel: RiskRead,
		Provider:  "missing",
	}, ToolCallRequest{})

	if response.Status != "error" {
		t.Fatalf("expected error, got %q", response.Status)
	}
	if response.Reason == "" {
		t.Fatalf("expected error reason")
	}
}

func TestMockAdapterReturnsErrorForMissingFixture(t *testing.T) {
	adapter := NewMockAdapter(map[string]map[string]any{})
	_, err := adapter.Execute(CapabilityDefinition{
		ID: "metrics.get_service_health",
	}, ToolCallRequest{})
	if err == nil {
		t.Fatalf("expected missing fixture error")
	}
}

func TestGitHubAdapterRequiresToken(t *testing.T) {
	adapter := NewGitHubAdapter(GitHubAdapterConfig{})
	_, err := adapter.Execute(CapabilityDefinition{
		ID:       "ci.get_checks",
		Provider: GitHubProvider,
	}, ToolCallRequest{})
	if err == nil {
		t.Fatalf("expected missing token error")
	}
}

func TestGitHubAdapterReportsSkeletonForUnimplementedSupportedCapabilities(t *testing.T) {
	adapter := NewGitHubAdapter(GitHubAdapterConfig{
		Token: "test-token",
	})
	_, err := adapter.Execute(CapabilityDefinition{
		ID:       "code_host.create_draft_pr",
		Provider: GitHubProvider,
	}, ToolCallRequest{})
	if err == nil {
		t.Fatalf("expected skeleton implementation error")
	}
	if err.Error() != "github adapter live execution is not implemented for 'code_host.create_draft_pr' yet" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGitHubAdapterGetsRecentChanges(t *testing.T) {
	var sawAuth bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/backend/pulls" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("state") != "closed" {
			t.Fatalf("expected closed pull requests query")
		}
		if r.URL.Query().Get("per_page") != "4" {
			t.Fatalf("expected doubled per_page for filtering, got %q", r.URL.Query().Get("per_page"))
		}
		if r.URL.Query().Get("base") != "main" {
			t.Fatalf("expected base branch")
		}
		if r.Header.Get("Authorization") == "Bearer test-token" {
			sawAuth = true
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[
			{
				"number": 10,
				"title": "Tune database pool defaults",
				"html_url": "https://github.com/acme/backend/pull/10",
				"merged_at": "2026-07-09T09:38:00Z",
				"updated_at": "2026-07-09T09:39:00Z",
				"changed_files": 2,
				"user": {"login": "octocat"}
			},
			{
				"number": 9,
				"title": "Closed but not merged",
				"html_url": "https://github.com/acme/backend/pull/9",
				"merged_at": null,
				"updated_at": "2026-07-09T09:00:00Z",
				"changed_files": 1,
				"user": {"login": "octocat"}
			},
			{
				"number": 8,
				"title": "Update retry timeout",
				"html_url": "https://github.com/acme/backend/pull/8",
				"merged_at": "2026-07-09T08:00:00Z",
				"updated_at": "2026-07-09T08:01:00Z",
				"changed_files": 4,
				"user": {"login": "hubot"}
			}
		]`))
	}))
	defer server.Close()

	adapter := NewGitHubAdapter(GitHubAdapterConfig{
		Token:   "test-token",
		BaseURL: server.URL,
		Client:  server.Client(),
	})
	result, err := adapter.Execute(CapabilityDefinition{
		ID:       "code_host.get_recent_changes",
		Provider: GitHubProvider,
	}, ToolCallRequest{
		Arguments: map[string]any{
			"repository": "acme/backend",
			"branch":     "main",
			"limit":      2,
		},
	})
	if err != nil {
		t.Fatalf("expected recent changes result, got error: %v", err)
	}
	if !sawAuth {
		t.Fatalf("expected authorization header")
	}
	changes, ok := result["changes"].([]map[string]any)
	if !ok {
		t.Fatalf("expected normalized changes")
	}
	if len(changes) != 2 {
		t.Fatalf("expected two merged changes, got %d", len(changes))
	}
	if changes[0]["pr"] != 10 {
		t.Fatalf("expected first PR #10, got %#v", changes[0]["pr"])
	}
	if changes[1]["pr"] != 8 {
		t.Fatalf("expected second merged PR #8, got %#v", changes[1]["pr"])
	}
	if result["evidence"] != "GitHub returned 2 merged pull request change(s) for acme/backend." {
		t.Fatalf("unexpected evidence: %#v", result["evidence"])
	}
}

func TestGitHubAdapterGetsChecksForCommitRef(t *testing.T) {
	var sawAuth bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/backend/commits/sha123/check-runs" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") == "Bearer test-token" {
			sawAuth = true
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"total_count": 2,
			"check_runs": [
				{"name": "unit-tests", "status": "completed", "conclusion": "success", "html_url": "https://github.com/acme/backend/runs/1"},
				{"name": "lint", "status": "completed", "conclusion": "success", "html_url": "https://github.com/acme/backend/runs/2"}
			]
		}`))
	}))
	defer server.Close()

	adapter := NewGitHubAdapter(GitHubAdapterConfig{
		Token:   "test-token",
		BaseURL: server.URL,
		Client:  server.Client(),
	})
	result, err := adapter.Execute(CapabilityDefinition{
		ID:       "ci.get_checks",
		Provider: GitHubProvider,
	}, ToolCallRequest{
		Arguments: map[string]any{
			"repository": "acme/backend",
			"commit_sha": "sha123",
		},
	})
	if err != nil {
		t.Fatalf("expected checks result, got error: %v", err)
	}
	if !sawAuth {
		t.Fatalf("expected authorization header")
	}
	if result["status"] != "passed" {
		t.Fatalf("expected passed status, got %#v", result["status"])
	}
	if result["commit_sha"] != "sha123" {
		t.Fatalf("expected commit SHA in result")
	}
	checks, ok := result["checks"].([]map[string]any)
	if !ok {
		t.Fatalf("expected normalized checks")
	}
	if len(checks) != 2 {
		t.Fatalf("expected two checks, got %d", len(checks))
	}
}

func TestGitHubAdapterGetsChecksForPullRequestNumber(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/acme/backend/pulls/42":
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"head": {"sha": "head-sha-42"}}`))
		case "/repos/acme/backend/commits/head-sha-42/check-runs":
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{
				"total_count": 1,
				"check_runs": [
					{"name": "unit-tests", "status": "completed", "conclusion": "failure", "html_url": "https://github.com/acme/backend/runs/3"}
				]
			}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	adapter := NewGitHubAdapter(GitHubAdapterConfig{
		Token:   "test-token",
		BaseURL: server.URL,
		Client:  server.Client(),
	})
	result, err := adapter.Execute(CapabilityDefinition{
		ID:       "ci.get_checks",
		Provider: GitHubProvider,
	}, ToolCallRequest{
		Arguments: map[string]any{
			"owner":     "acme",
			"repo":      "backend",
			"pr_number": 42,
		},
	})
	if err != nil {
		t.Fatalf("expected PR checks result, got error: %v", err)
	}
	if result["status"] != "failed" {
		t.Fatalf("expected failed status, got %#v", result["status"])
	}
	if result["commit_sha"] != "head-sha-42" {
		t.Fatalf("expected PR head SHA, got %#v", result["commit_sha"])
	}
}

func TestGitHubAdapterGetsLogsFromLogsURL(t *testing.T) {
	var sawAuth bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/logs/job-1" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") == "Bearer test-token" {
			sawAuth = true
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("unit-tests\nerror: assertion failed\n"))
	}))
	defer server.Close()

	adapter := NewGitHubAdapter(GitHubAdapterConfig{
		Token:   "test-token",
		BaseURL: server.URL,
		Client:  server.Client(),
	})
	result, err := adapter.Execute(CapabilityDefinition{
		ID:       "ci.get_logs",
		Provider: GitHubProvider,
	}, ToolCallRequest{
		Arguments: map[string]any{
			"logs_url": server.URL + "/logs/job-1",
		},
	})
	if err != nil {
		t.Fatalf("expected logs result, got error: %v", err)
	}
	if !sawAuth {
		t.Fatalf("expected authorization header")
	}
	if result["summary"] != "GitHub CI logs contain failure indicators." {
		t.Fatalf("expected failure summary, got %#v", result["summary"])
	}
	if result["log_excerpt"] != "unit-tests\nerror: assertion failed\n" {
		t.Fatalf("expected log excerpt, got %#v", result["log_excerpt"])
	}
	if result["truncated"] != false {
		t.Fatalf("expected logs to fit without truncation")
	}
}

func TestGitHubAdapterGetsLogsFromJobID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/backend/actions/jobs/123/logs" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("build\nok\n"))
	}))
	defer server.Close()

	adapter := NewGitHubAdapter(GitHubAdapterConfig{
		Token:   "test-token",
		BaseURL: server.URL,
		Client:  server.Client(),
	})
	result, err := adapter.Execute(CapabilityDefinition{
		ID:       "ci.get_logs",
		Provider: GitHubProvider,
	}, ToolCallRequest{
		Arguments: map[string]any{
			"repository": "acme/backend",
			"job_id":     123,
		},
	})
	if err != nil {
		t.Fatalf("expected job logs result, got error: %v", err)
	}
	if result["summary"] != "GitHub CI logs fetched without obvious failure indicators." {
		t.Fatalf("expected clean summary, got %#v", result["summary"])
	}
	if result["source_url"] != server.URL+"/repos/acme/backend/actions/jobs/123/logs" {
		t.Fatalf("expected source URL, got %#v", result["source_url"])
	}
}

func TestGitHubProviderOverridesCodeAndCICapabilities(t *testing.T) {
	registry := DefaultCapabilityRegistry().WithProviderOverrides(GitHubProviderOverrides())
	for _, id := range []string{
		"code_host.get_recent_changes",
		"code_host.create_draft_pr",
		"ci.get_checks",
		"ci.get_logs",
	} {
		definition, ok := registry.byID[id]
		if !ok {
			t.Fatalf("expected capability %q", id)
		}
		if definition.Provider != GitHubProvider {
			t.Fatalf("expected %q provider for %q, got %q", GitHubProvider, id, definition.Provider)
		}
	}

	definition, ok := registry.byID["metrics.get_service_health"]
	if !ok {
		t.Fatalf("expected metrics capability")
	}
	if definition.Provider != "mock" {
		t.Fatalf("expected metrics provider to remain mock, got %q", definition.Provider)
	}
}
