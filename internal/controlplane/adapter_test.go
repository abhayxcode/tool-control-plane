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
		ID:       "ci.get_logs",
		Provider: GitHubProvider,
	}, ToolCallRequest{})
	if err == nil {
		t.Fatalf("expected skeleton implementation error")
	}
	if err.Error() != "github adapter live execution is not implemented for 'ci.get_logs' yet" {
		t.Fatalf("unexpected error: %v", err)
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
