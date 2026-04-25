package controlplane

import "testing"

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

func TestGitHubAdapterReportsSkeletonForSupportedCapabilities(t *testing.T) {
	adapter := NewGitHubAdapter(GitHubAdapterConfig{
		Token: "test-token",
	})
	_, err := adapter.Execute(CapabilityDefinition{
		ID:       "ci.get_checks",
		Provider: GitHubProvider,
	}, ToolCallRequest{})
	if err == nil {
		t.Fatalf("expected skeleton implementation error")
	}
	if err.Error() != "github adapter live execution is not implemented for 'ci.get_checks' yet" {
		t.Fatalf("unexpected error: %v", err)
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
