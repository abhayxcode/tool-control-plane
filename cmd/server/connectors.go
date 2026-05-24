package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/abhayxcode/tool-control-plane/internal/controlplane"
)

func connectorList(svc *controlplane.Service, config Config) []controlplane.Connector {
	connectors := configuredConnectors(config)
	connectors = append(connectors, svc.Connectors()...)
	return connectors
}

func configuredConnectors(config Config) []controlplane.Connector {
	codeProvider := providerOrMock(config.CodeProvider)
	return []controlplane.Connector{
		configuredConnector(config, "code_host", codeProvider),
		configuredConnector(config, "ci", codeProvider),
		configuredConnector(config, "deploy", providerOrMock(config.DeployProvider)),
		configuredConnector(config, "errors", providerOrMock(config.ErrorsProvider)),
		configuredConnector(config, "metrics", providerOrMock(config.MetricsProvider)),
		configuredConnector(config, "runtime", providerOrMock(config.RuntimeProvider)),
		configuredConnector(config, "docs", providerOrMock(config.DocsProvider)),
		configuredConnector(config, "internal_api", providerOrMock(config.InternalAPIProvider)),
	}
}

func configuredConnector(config Config, capability string, provider string) controlplane.Connector {
	source := "env"
	if provider == "mock" {
		source = "default"
	}
	return controlplane.Connector{
		ID:         configuredConnectorID(capability, provider),
		OrgID:      "default",
		Name:       fmt.Sprintf("%s %s", strings.ToUpper(provider), capability),
		Provider:   provider,
		Capability: capability,
		Config:     connectorPublicConfig(config, capability, provider),
		SecretRef:  connectorSecretRef(config, provider),
		Status:     connectorStatus(config, provider),
		Source:     source,
	}
}

func configuredConnectorID(capability string, provider string) string {
	safeCapability := strings.NewReplacer(".", "_", "-", "_", " ", "_").Replace(capability)
	safeProvider := strings.NewReplacer(".", "_", "-", "_", " ", "_").Replace(provider)
	return "connector_config_" + safeCapability + "_" + safeProvider
}

func connectorPublicConfig(config Config, capability string, provider string) map[string]any {
	switch provider {
	case "mock":
		return map[string]any{
			"fixture_provider": true,
		}
	case controlplane.GitHubProvider:
		return map[string]any{
			"auth_mode":         githubAuthMode(config),
			"base_url_set":      strings.TrimSpace(config.GitHubBaseURL) != "",
			"max_attempts":      githubMaxAttempts(config),
			"retry_backoff_ms":  int(githubRetryBackoff(config) / time.Millisecond),
			"demo_repository":   strings.TrimSpace(config.DemoRepository),
			"capability_source": capability,
		}
	case controlplane.SentryProvider:
		return map[string]any{
			"base_url_set":        strings.TrimSpace(config.SentryBaseURL) != "",
			"default_org_set":     strings.TrimSpace(config.SentryOrg) != "",
			"default_project_set": strings.TrimSpace(config.SentryProject) != "",
		}
	case controlplane.PrometheusProvider:
		return map[string]any{
			"base_url_set":      strings.TrimSpace(config.PrometheusBaseURL) != "",
			"service_label":     firstConfiguredLabel(config.PrometheusServiceLabel, "service"),
			"environment_label": firstConfiguredLabel(config.PrometheusEnvLabel, "environment"),
			"status_label":      firstConfiguredLabel(config.PrometheusStatusLabel, "status"),
		}
	case controlplane.KubernetesProvider:
		return map[string]any{
			"base_url_set":      strings.TrimSpace(config.KubernetesBaseURL) != "",
			"namespace":         firstConfiguredLabel(config.KubernetesNamespace, "default"),
			"label_selector":    strings.TrimSpace(config.KubernetesLabelSelector),
			"service_label":     firstConfiguredLabel(config.KubernetesServiceLabel, "app"),
			"environment_label": strings.TrimSpace(config.KubernetesEnvLabel),
		}
	case controlplane.GenericHTTPProvider:
		return map[string]any{
			"base_url_set":       strings.TrimSpace(config.GenericHTTPBaseURL) != "",
			"allowed_methods":    genericHTTPAllowedMethods(config),
			"timeout_ms":         int(genericHTTPTimeout(config) / time.Millisecond),
			"max_response_bytes": genericHTTPMaxResponseBytes(config),
		}
	default:
		return map[string]any{}
	}
}

func connectorSecretRef(config Config, provider string) string {
	switch provider {
	case controlplane.GitHubProvider:
		if ref := configuredSecretRef(config.GitHubTokenRef, "GITHUB_TOKEN", config.GitHubToken); ref != "" {
			return ref
		}
		if githubAppConfigured(config) {
			if strings.TrimSpace(config.GitHubAppPrivateKeyRef) != "" {
				return strings.TrimSpace(config.GitHubAppPrivateKeyRef)
			}
			if strings.TrimSpace(config.GitHubAppPrivateKeyPath) != "" {
				return controlplane.SecretRefFilePrefix + strings.TrimSpace(config.GitHubAppPrivateKeyPath)
			}
			if strings.TrimSpace(config.GitHubAppPrivateKey) != "" {
				return controlplane.SecretRefEnvPrefix + "GITHUB_APP_PRIVATE_KEY"
			}
		}
	case controlplane.SentryProvider:
		if ref := configuredSecretRef(config.SentryAuthTokenRef, "SENTRY_AUTH_TOKEN", config.SentryAuthToken); ref != "" {
			return ref
		}
	case controlplane.PrometheusProvider:
		if ref := configuredSecretRef(config.PrometheusBearerTokenRef, "PROMETHEUS_BEARER_TOKEN", config.PrometheusBearerToken); ref != "" {
			return ref
		}
	case controlplane.KubernetesProvider:
		if ref := configuredSecretRef(config.KubernetesBearerTokenRef, "KUBERNETES_BEARER_TOKEN", config.KubernetesBearerToken); ref != "" {
			return ref
		}
	case controlplane.GenericHTTPProvider:
		if ref := configuredSecretRef(config.GenericHTTPBearerTokenRef, "GENERIC_HTTP_BEARER_TOKEN", config.GenericHTTPBearerToken); ref != "" {
			return ref
		}
	}
	return ""
}

func configuredSecretRef(ref string, envName string, value string) string {
	if strings.TrimSpace(ref) != "" {
		return strings.TrimSpace(ref)
	}
	if strings.TrimSpace(value) != "" {
		return controlplane.SecretRefEnvPrefix + envName
	}
	return ""
}

func connectorStatus(config Config, provider string) string {
	switch provider {
	case "mock":
		return controlplane.ConnectorStatusReady
	case controlplane.GitHubProvider:
		if githubCredentialConfigured(config) {
			return controlplane.ConnectorStatusReady
		}
	case controlplane.SentryProvider:
		if strings.TrimSpace(config.SentryAuthToken) != "" {
			return controlplane.ConnectorStatusReady
		}
	case controlplane.PrometheusProvider:
		if strings.TrimSpace(config.PrometheusBaseURL) != "" {
			return controlplane.ConnectorStatusReady
		}
	case controlplane.KubernetesProvider:
		if strings.TrimSpace(config.KubernetesBaseURL) != "" {
			return controlplane.ConnectorStatusReady
		}
	case controlplane.GenericHTTPProvider:
		if strings.TrimSpace(config.GenericHTTPBaseURL) != "" {
			return controlplane.ConnectorStatusReady
		}
	}
	return controlplane.ConnectorStatusBlocked
}
