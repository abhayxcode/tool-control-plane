package controlplane

import "fmt"

type ToolAdapter interface {
	Execute(definition CapabilityDefinition, req ToolCallRequest) (map[string]any, error)
}

type AdapterRegistry struct {
	byProvider map[string]ToolAdapter
}

func NewAdapterRegistry(adapters map[string]ToolAdapter) AdapterRegistry {
	byProvider := make(map[string]ToolAdapter, len(adapters))
	for provider, adapter := range adapters {
		byProvider[provider] = adapter
	}
	return AdapterRegistry{byProvider: byProvider}
}

func (r AdapterRegistry) Execute(definition CapabilityDefinition, req ToolCallRequest) ToolCallResponse {
	adapter, ok := r.byProvider[definition.Provider]
	if !ok {
		return ToolCallResponse{
			Status:    "error",
			RiskLevel: definition.RiskLevel,
			Reason:    fmt.Sprintf("No adapter registered for provider '%s'.", definition.Provider),
		}
	}

	result, err := adapter.Execute(definition, req)
	if err != nil {
		return ToolCallResponse{
			Status:    "error",
			RiskLevel: definition.RiskLevel,
			Provider:  definition.Provider,
			Reason:    err.Error(),
		}
	}

	return ToolCallResponse{
		Status:    "success",
		RiskLevel: definition.RiskLevel,
		Provider:  definition.Provider,
		Result:    result,
	}
}

func DefaultAdapterRegistry() AdapterRegistry {
	return NewAdapterRegistry(map[string]ToolAdapter{
		"mock": NewMockAdapter(defaultMockFixtures()),
	})
}

func DefaultAdapterRegistryWithGitHub(config GitHubAdapterConfig) AdapterRegistry {
	return NewAdapterRegistry(map[string]ToolAdapter{
		"mock":   NewMockAdapter(defaultMockFixtures()),
		"github": NewGitHubAdapter(config),
	})
}
