package controlplane

import (
	"errors"
	"fmt"
	"sort"
)

type ToolAdapter interface {
	Execute(definition CapabilityDefinition, req ToolCallRequest) (map[string]any, error)
}

type AdapterRegistry struct {
	byProvider map[string]ToolAdapter
}

type toolCallErrorDetailer interface {
	ToolCallError() ToolCallError
}

func NewAdapterRegistry(adapters map[string]ToolAdapter) AdapterRegistry {
	byProvider := make(map[string]ToolAdapter, len(adapters))
	for provider, adapter := range adapters {
		byProvider[provider] = adapter
	}
	return AdapterRegistry{byProvider: byProvider}
}

func (r AdapterRegistry) Providers() []string {
	providers := make([]string, 0, len(r.byProvider))
	for provider := range r.byProvider {
		providers = append(providers, provider)
	}
	sort.Strings(providers)
	return providers
}

func (r AdapterRegistry) HasProvider(provider string) bool {
	_, ok := r.byProvider[provider]
	return ok
}

func (r AdapterRegistry) Execute(definition CapabilityDefinition, req ToolCallRequest) ToolCallResponse {
	adapter, ok := r.byProvider[definition.Provider]
	if !ok {
		return ToolCallResponse{
			Status:    "error",
			RiskLevel: definition.RiskLevel,
			Provider:  definition.Provider,
			Reason:    fmt.Sprintf("No adapter registered for provider '%s'.", definition.Provider),
		}
	}

	result, err := adapter.Execute(definition, req)
	if err != nil {
		toolCallError := ToolCallError{
			Provider: definition.Provider,
			Category: "provider_error",
			Message:  err.Error(),
		}
		var detailer toolCallErrorDetailer
		if errors.As(err, &detailer) {
			toolCallError = detailer.ToolCallError()
		}
		return ToolCallResponse{
			Status:    "error",
			RiskLevel: definition.RiskLevel,
			Provider:  definition.Provider,
			Reason:    err.Error(),
			Error:     &toolCallError,
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

type AdapterRegistryOptions struct {
	GitHub      *GitHubAdapterConfig
	Sentry      *SentryAdapterConfig
	Prometheus  *PrometheusAdapterConfig
	Kubernetes  *KubernetesAdapterConfig
	GenericHTTP *GenericHTTPAdapterConfig
}

func DefaultAdapterRegistryWithOptions(options AdapterRegistryOptions) AdapterRegistry {
	adapters := map[string]ToolAdapter{
		"mock": NewMockAdapter(defaultMockFixtures()),
	}
	if options.GitHub != nil {
		adapters[GitHubProvider] = NewGitHubAdapter(*options.GitHub)
	}
	if options.Sentry != nil {
		adapters[SentryProvider] = NewSentryAdapter(*options.Sentry)
	}
	if options.Prometheus != nil {
		adapters[PrometheusProvider] = NewPrometheusAdapter(*options.Prometheus)
	}
	if options.Kubernetes != nil {
		adapters[KubernetesProvider] = NewKubernetesAdapter(*options.Kubernetes)
	}
	if options.GenericHTTP != nil {
		adapters[GenericHTTPProvider] = NewGenericHTTPAdapter(*options.GenericHTTP)
	}
	return NewAdapterRegistry(adapters)
}

func DefaultAdapterRegistryWithGitHub(config GitHubAdapterConfig) AdapterRegistry {
	return DefaultAdapterRegistryWithOptions(AdapterRegistryOptions{GitHub: &config})
}
