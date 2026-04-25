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
