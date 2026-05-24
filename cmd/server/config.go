package main

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/abhayxcode/tool-control-plane/internal/controlplane"
)

type Config struct {
	Addr                        string
	ShutdownTimeout             time.Duration
	APIToken                    string
	APITokenRef                 string
	RateLimitPerMinute          int
	Store                       string
	SQLitePath                  string
	PolicyFile                  string
	CodeProvider                string
	DeployProvider              string
	ErrorsProvider              string
	MetricsProvider             string
	RuntimeProvider             string
	DocsProvider                string
	InternalAPIProvider         string
	GitHubToken                 string
	GitHubTokenRef              string
	GitHubAppID                 string
	GitHubAppInstallationID     string
	GitHubAppPrivateKey         string
	GitHubAppPrivateKeyRef      string
	GitHubAppPrivateKeyPath     string
	GitHubBaseURL               string
	GitHubMaxAttempts           int
	GitHubRetryBackoff          time.Duration
	SentryAuthToken             string
	SentryAuthTokenRef          string
	SentryOrg                   string
	SentryProject               string
	SentryBaseURL               string
	PrometheusBaseURL           string
	PrometheusBearerToken       string
	PrometheusBearerTokenRef    string
	PrometheusServiceLabel      string
	PrometheusEnvLabel          string
	PrometheusStatusLabel       string
	KubernetesBaseURL           string
	KubernetesBearerToken       string
	KubernetesBearerTokenRef    string
	KubernetesNamespace         string
	KubernetesLabelSelector     string
	KubernetesServiceLabel      string
	KubernetesEnvLabel          string
	GenericHTTPBaseURL          string
	GenericHTTPBearerToken      string
	GenericHTTPBearerTokenRef   string
	GenericHTTPAllowedMethods   []string
	GenericHTTPTimeout          time.Duration
	GenericHTTPMaxResponseBytes int
	DemoRepository              string
	SecretBroker                controlplane.SecretBroker
}

func configFromEnv() (Config, error) {
	broker := controlplane.LocalSecretBroker{}
	apiToken, apiTokenRef, err := secretFromEnv(broker, "TOOL_CONTROL_PLANE_API_TOKEN", "TOOL_CONTROL_PLANE_API_TOKEN_REF")
	if err != nil {
		return Config{}, err
	}
	githubToken, githubTokenRef, err := secretFromEnv(broker, "GITHUB_TOKEN", "GITHUB_TOKEN_REF")
	if err != nil {
		return Config{}, err
	}
	githubAppPrivateKey, githubAppPrivateKeyRef, err := secretFromEnv(broker, "GITHUB_APP_PRIVATE_KEY", "GITHUB_APP_PRIVATE_KEY_REF")
	if err != nil {
		return Config{}, err
	}
	sentryAuthToken, sentryAuthTokenRef, err := secretFromEnv(broker, "SENTRY_AUTH_TOKEN", "SENTRY_AUTH_TOKEN_REF")
	if err != nil {
		return Config{}, err
	}
	prometheusBearerToken, prometheusBearerTokenRef, err := secretFromEnv(broker, "PROMETHEUS_BEARER_TOKEN", "PROMETHEUS_BEARER_TOKEN_REF")
	if err != nil {
		return Config{}, err
	}
	kubernetesBearerToken, kubernetesBearerTokenRef, err := secretFromEnv(broker, "KUBERNETES_BEARER_TOKEN", "KUBERNETES_BEARER_TOKEN_REF")
	if err != nil {
		return Config{}, err
	}
	genericHTTPBearerToken, genericHTTPBearerTokenRef, err := secretFromEnv(broker, "GENERIC_HTTP_BEARER_TOKEN", "GENERIC_HTTP_BEARER_TOKEN_REF")
	if err != nil {
		return Config{}, err
	}
	config := Config{
		Addr:                      envOrDefault("TOOL_CONTROL_PLANE_ADDR", ":4100"),
		ShutdownTimeout:           10 * time.Second,
		APIToken:                  apiToken,
		APITokenRef:               apiTokenRef,
		Store:                     os.Getenv("TOOL_CONTROL_PLANE_STORE"),
		SQLitePath:                os.Getenv("TOOL_CONTROL_PLANE_SQLITE_PATH"),
		PolicyFile:                os.Getenv("TOOL_CONTROL_PLANE_POLICY_FILE"),
		CodeProvider:              os.Getenv("TOOL_CONTROL_PLANE_CODE_PROVIDER"),
		DeployProvider:            os.Getenv("TOOL_CONTROL_PLANE_DEPLOY_PROVIDER"),
		ErrorsProvider:            os.Getenv("TOOL_CONTROL_PLANE_ERRORS_PROVIDER"),
		MetricsProvider:           os.Getenv("TOOL_CONTROL_PLANE_METRICS_PROVIDER"),
		RuntimeProvider:           os.Getenv("TOOL_CONTROL_PLANE_RUNTIME_PROVIDER"),
		DocsProvider:              os.Getenv("TOOL_CONTROL_PLANE_DOCS_PROVIDER"),
		InternalAPIProvider:       os.Getenv("TOOL_CONTROL_PLANE_INTERNAL_API_PROVIDER"),
		GitHubToken:               githubToken,
		GitHubTokenRef:            githubTokenRef,
		GitHubAppID:               os.Getenv("GITHUB_APP_ID"),
		GitHubAppInstallationID:   os.Getenv("GITHUB_APP_INSTALLATION_ID"),
		GitHubAppPrivateKey:       normalizeGitHubAppPrivateKey(githubAppPrivateKey),
		GitHubAppPrivateKeyRef:    githubAppPrivateKeyRef,
		GitHubAppPrivateKeyPath:   os.Getenv("GITHUB_APP_PRIVATE_KEY_PATH"),
		GitHubBaseURL:             os.Getenv("GITHUB_API_BASE_URL"),
		SentryAuthToken:           sentryAuthToken,
		SentryAuthTokenRef:        sentryAuthTokenRef,
		SentryOrg:                 os.Getenv("SENTRY_ORG"),
		SentryProject:             os.Getenv("SENTRY_PROJECT"),
		SentryBaseURL:             os.Getenv("SENTRY_BASE_URL"),
		PrometheusBaseURL:         os.Getenv("PROMETHEUS_BASE_URL"),
		PrometheusBearerToken:     prometheusBearerToken,
		PrometheusBearerTokenRef:  prometheusBearerTokenRef,
		PrometheusServiceLabel:    os.Getenv("PROMETHEUS_SERVICE_LABEL"),
		PrometheusEnvLabel:        os.Getenv("PROMETHEUS_ENVIRONMENT_LABEL"),
		PrometheusStatusLabel:     os.Getenv("PROMETHEUS_STATUS_LABEL"),
		KubernetesBaseURL:         os.Getenv("KUBERNETES_BASE_URL"),
		KubernetesBearerToken:     kubernetesBearerToken,
		KubernetesBearerTokenRef:  kubernetesBearerTokenRef,
		KubernetesNamespace:       os.Getenv("KUBERNETES_NAMESPACE"),
		KubernetesLabelSelector:   os.Getenv("KUBERNETES_LABEL_SELECTOR"),
		KubernetesServiceLabel:    os.Getenv("KUBERNETES_SERVICE_LABEL"),
		KubernetesEnvLabel:        os.Getenv("KUBERNETES_ENVIRONMENT_LABEL"),
		GenericHTTPBaseURL:        os.Getenv("GENERIC_HTTP_BASE_URL"),
		GenericHTTPBearerToken:    genericHTTPBearerToken,
		GenericHTTPBearerTokenRef: genericHTTPBearerTokenRef,
		GenericHTTPAllowedMethods: parseCSVEnv(os.Getenv("GENERIC_HTTP_ALLOWED_METHODS")),
		DemoRepository:            os.Getenv("TOOL_CONTROL_PLANE_DEMO_REPOSITORY"),
		SecretBroker:              broker,
	}
	rawShutdownTimeout := strings.TrimSpace(os.Getenv("TOOL_CONTROL_PLANE_SHUTDOWN_TIMEOUT"))
	if rawShutdownTimeout != "" {
		timeout, err := time.ParseDuration(rawShutdownTimeout)
		if err != nil {
			return Config{}, fmt.Errorf("invalid TOOL_CONTROL_PLANE_SHUTDOWN_TIMEOUT: %w", err)
		}
		config.ShutdownTimeout = timeout
	}
	rawRateLimit := strings.TrimSpace(os.Getenv("TOOL_CONTROL_PLANE_RATE_LIMIT_PER_MINUTE"))
	if rawRateLimit != "" {
		limit, err := strconv.Atoi(rawRateLimit)
		if err != nil {
			return Config{}, fmt.Errorf("invalid TOOL_CONTROL_PLANE_RATE_LIMIT_PER_MINUTE: %w", err)
		}
		config.RateLimitPerMinute = limit
	}
	rawGitHubMaxAttempts := strings.TrimSpace(os.Getenv("TOOL_CONTROL_PLANE_GITHUB_MAX_ATTEMPTS"))
	if rawGitHubMaxAttempts != "" {
		maxAttempts, err := strconv.Atoi(rawGitHubMaxAttempts)
		if err != nil || maxAttempts <= 0 {
			return Config{}, fmt.Errorf("invalid TOOL_CONTROL_PLANE_GITHUB_MAX_ATTEMPTS: must be a positive integer")
		}
		config.GitHubMaxAttempts = maxAttempts
	}
	rawGitHubRetryBackoff := strings.TrimSpace(os.Getenv("TOOL_CONTROL_PLANE_GITHUB_RETRY_BACKOFF"))
	if rawGitHubRetryBackoff != "" {
		backoff, err := time.ParseDuration(rawGitHubRetryBackoff)
		if err != nil || backoff < 0 {
			return Config{}, fmt.Errorf("invalid TOOL_CONTROL_PLANE_GITHUB_RETRY_BACKOFF: must be a non-negative duration")
		}
		config.GitHubRetryBackoff = backoff
	}
	rawGenericHTTPTimeout := strings.TrimSpace(os.Getenv("GENERIC_HTTP_TIMEOUT"))
	if rawGenericHTTPTimeout != "" {
		timeout, err := time.ParseDuration(rawGenericHTTPTimeout)
		if err != nil || timeout <= 0 {
			return Config{}, fmt.Errorf("invalid GENERIC_HTTP_TIMEOUT: must be a positive duration")
		}
		config.GenericHTTPTimeout = timeout
	}
	rawGenericHTTPMaxResponseBytes := strings.TrimSpace(os.Getenv("GENERIC_HTTP_MAX_RESPONSE_BYTES"))
	if rawGenericHTTPMaxResponseBytes != "" {
		limit, err := strconv.Atoi(rawGenericHTTPMaxResponseBytes)
		if err != nil || limit <= 0 {
			return Config{}, fmt.Errorf("invalid GENERIC_HTTP_MAX_RESPONSE_BYTES: must be a positive integer")
		}
		config.GenericHTTPMaxResponseBytes = limit
	}
	return config, nil
}

func newServiceFromConfig(config Config) (*controlplane.Service, error) {
	registry := controlplane.DefaultCapabilityRegistry()
	policy := controlplane.PolicyEngine(controlplane.StaticPolicyEngine{})
	adapters := controlplane.DefaultAdapterRegistry()
	store := controlplane.Store(controlplane.NewMemoryStore())
	var githubConfig *controlplane.GitHubAdapterConfig
	var sentryConfig *controlplane.SentryAdapterConfig
	var prometheusConfig *controlplane.PrometheusAdapterConfig
	var kubernetesConfig *controlplane.KubernetesAdapterConfig
	var genericHTTPConfig *controlplane.GenericHTTPAdapterConfig
	overrides := map[string]string{}
	if config.CodeProvider == controlplane.GitHubProvider || config.DeployProvider == controlplane.GitHubProvider || config.DocsProvider == controlplane.GitHubProvider {
		if config.CodeProvider == controlplane.GitHubProvider {
			for id, provider := range controlplane.GitHubProviderOverrides() {
				overrides[id] = provider
			}
		}
		if config.DeployProvider == controlplane.GitHubProvider {
			for id, provider := range controlplane.GitHubDeployProviderOverrides() {
				overrides[id] = provider
			}
		}
		if config.DocsProvider == controlplane.GitHubProvider {
			for id, provider := range controlplane.GitHubDocsProviderOverrides() {
				overrides[id] = provider
			}
		}
		tokenSource, err := githubTokenSourceFromConfig(config, http.DefaultClient)
		if err != nil {
			return nil, err
		}
		githubConfig = &controlplane.GitHubAdapterConfig{
			Token:        config.GitHubToken,
			TokenSource:  tokenSource,
			BaseURL:      config.GitHubBaseURL,
			MaxAttempts:  config.GitHubMaxAttempts,
			RetryBackoff: config.GitHubRetryBackoff,
		}
	}
	if config.ErrorsProvider == controlplane.SentryProvider {
		for id, provider := range controlplane.SentryProviderOverrides() {
			overrides[id] = provider
		}
		sentryConfig = &controlplane.SentryAdapterConfig{
			Token:   config.SentryAuthToken,
			Org:     config.SentryOrg,
			Project: config.SentryProject,
			BaseURL: config.SentryBaseURL,
		}
	}
	if config.MetricsProvider == controlplane.PrometheusProvider {
		for id, provider := range controlplane.PrometheusProviderOverrides() {
			overrides[id] = provider
		}
		prometheusConfig = &controlplane.PrometheusAdapterConfig{
			BaseURL:          config.PrometheusBaseURL,
			BearerToken:      config.PrometheusBearerToken,
			ServiceLabel:     config.PrometheusServiceLabel,
			EnvironmentLabel: config.PrometheusEnvLabel,
			StatusLabel:      config.PrometheusStatusLabel,
		}
	}
	if config.RuntimeProvider == controlplane.KubernetesProvider {
		for id, provider := range controlplane.KubernetesProviderOverrides() {
			overrides[id] = provider
		}
		kubernetesConfig = &controlplane.KubernetesAdapterConfig{
			BaseURL:          config.KubernetesBaseURL,
			BearerToken:      config.KubernetesBearerToken,
			Namespace:        config.KubernetesNamespace,
			LabelSelector:    config.KubernetesLabelSelector,
			ServiceLabel:     config.KubernetesServiceLabel,
			EnvironmentLabel: config.KubernetesEnvLabel,
		}
	}
	if config.InternalAPIProvider == controlplane.GenericHTTPProvider {
		for id, provider := range controlplane.GenericHTTPProviderOverrides() {
			overrides[id] = provider
		}
		genericHTTPConfig = &controlplane.GenericHTTPAdapterConfig{
			BaseURL:          config.GenericHTTPBaseURL,
			BearerToken:      config.GenericHTTPBearerToken,
			AllowedMethods:   config.GenericHTTPAllowedMethods,
			Timeout:          config.GenericHTTPTimeout,
			MaxResponseBytes: config.GenericHTTPMaxResponseBytes,
		}
	}
	if len(overrides) > 0 {
		registry = registry.WithProviderOverrides(overrides)
		adapters = controlplane.DefaultAdapterRegistryWithOptions(controlplane.AdapterRegistryOptions{
			GitHub:      githubConfig,
			Sentry:      sentryConfig,
			Prometheus:  prometheusConfig,
			Kubernetes:  kubernetesConfig,
			GenericHTTP: genericHTTPConfig,
		})
	}
	if strings.TrimSpace(config.PolicyFile) != "" {
		data, err := os.ReadFile(config.PolicyFile)
		if err != nil {
			return nil, fmt.Errorf("read policy file: %w", err)
		}
		policyConfig, err := controlplane.ParsePolicyConfig(data)
		if err != nil {
			return nil, err
		}
		configuredPolicy, err := controlplane.NewRulePolicyEngine(policyConfig, policy)
		if err != nil {
			return nil, err
		}
		policy = configuredPolicy
	}
	if config.Store == "sqlite" || config.SQLitePath != "" {
		path := config.SQLitePath
		if path == "" {
			path = "tool-control-plane.sqlite3"
		}
		sqliteStore, err := controlplane.NewSQLiteStore(path)
		if err != nil {
			return nil, fmt.Errorf("open sqlite store: %w", err)
		}
		store = sqliteStore
	}
	return controlplane.NewServiceWithOptions(controlplane.ServiceOptions{
		Registry: registry,
		Policy:   policy,
		Adapters: adapters,
		Store:    store,
	}), nil
}

func newHandler(config Config, svc *controlplane.Service) http.Handler {
	return withRequestLogging(withRateLimit(withBearerAuth(newMux(svc, config), config.APIToken), newRateLimiter(config.RateLimitPerMinute, time.Minute)))
}

func secretBrokerFromConfig(config Config) controlplane.SecretBroker {
	if config.SecretBroker != nil {
		return config.SecretBroker
	}
	return controlplane.LocalSecretBroker{}
}

func secretFromEnv(broker controlplane.SecretBroker, valueKey string, refKey string) (string, string, error) {
	ref := strings.TrimSpace(os.Getenv(refKey))
	if ref != "" {
		value, err := broker.ResolveSecret(ref)
		if err != nil {
			return "", "", fmt.Errorf("resolve %s: %w", refKey, err)
		}
		if strings.TrimSpace(value) == "" {
			return "", "", fmt.Errorf("resolve %s: secret ref %q resolved empty", refKey, ref)
		}
		return value, ref, nil
	}
	value := os.Getenv(valueKey)
	if strings.TrimSpace(value) == "" {
		return value, "", nil
	}
	return value, controlplane.SecretRefEnvPrefix + valueKey, nil
}

func envOrDefault(key string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func parseCSVEnv(value string) []string {
	parts := strings.Split(value, ",")
	result := []string{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

func githubTokenSourceFromConfig(config Config, client *http.Client) (controlplane.GitHubTokenSource, error) {
	if strings.TrimSpace(config.GitHubToken) != "" {
		return controlplane.StaticGitHubTokenSource{TokenValue: config.GitHubToken}, nil
	}
	if !githubAppConfigured(config) {
		return controlplane.StaticGitHubTokenSource{}, nil
	}
	privateKey, err := githubAppPrivateKey(config)
	if err != nil {
		return nil, err
	}
	return controlplane.NewGitHubAppTokenSource(controlplane.GitHubAppTokenSourceConfig{
		AppID:          config.GitHubAppID,
		InstallationID: config.GitHubAppInstallationID,
		PrivateKeyPEM:  privateKey,
		BaseURL:        config.GitHubBaseURL,
		Client:         client,
	})
}

func githubAppConfigured(config Config) bool {
	return strings.TrimSpace(config.GitHubAppID) != "" &&
		strings.TrimSpace(config.GitHubAppInstallationID) != "" &&
		(strings.TrimSpace(config.GitHubAppPrivateKey) != "" ||
			strings.TrimSpace(config.GitHubAppPrivateKeyRef) != "" ||
			strings.TrimSpace(config.GitHubAppPrivateKeyPath) != "")
}

func githubAppPrivateKey(config Config) (string, error) {
	if strings.TrimSpace(config.GitHubAppPrivateKey) != "" {
		return normalizeGitHubAppPrivateKey(config.GitHubAppPrivateKey), nil
	}
	if strings.TrimSpace(config.GitHubAppPrivateKeyRef) != "" {
		value, err := secretBrokerFromConfig(config).ResolveSecret(config.GitHubAppPrivateKeyRef)
		if err != nil {
			return "", fmt.Errorf("resolve GITHUB_APP_PRIVATE_KEY_REF: %w", err)
		}
		return normalizeGitHubAppPrivateKey(value), nil
	}
	path := strings.TrimSpace(config.GitHubAppPrivateKeyPath)
	if path == "" {
		return "", nil
	}
	content, err := secretBrokerFromConfig(config).ResolveSecret(controlplane.SecretRefFilePrefix + path)
	if err != nil {
		return "", fmt.Errorf("read GITHUB_APP_PRIVATE_KEY_PATH: %w", err)
	}
	return normalizeGitHubAppPrivateKey(content), nil
}

func normalizeGitHubAppPrivateKey(value string) string {
	return strings.ReplaceAll(value, `\n`, "\n")
}
