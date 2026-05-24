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
	Addr                    string
	ShutdownTimeout         time.Duration
	APIToken                string
	RateLimitPerMinute      int
	Store                   string
	SQLitePath              string
	CodeProvider            string
	DeployProvider          string
	GitHubToken             string
	GitHubAppID             string
	GitHubAppInstallationID string
	GitHubAppPrivateKey     string
	GitHubAppPrivateKeyPath string
	GitHubBaseURL           string
	GitHubMaxAttempts       int
	GitHubRetryBackoff      time.Duration
	DemoRepository          string
}

func configFromEnv() (Config, error) {
	config := Config{
		Addr:                    envOrDefault("TOOL_CONTROL_PLANE_ADDR", ":4100"),
		ShutdownTimeout:         10 * time.Second,
		APIToken:                os.Getenv("TOOL_CONTROL_PLANE_API_TOKEN"),
		Store:                   os.Getenv("TOOL_CONTROL_PLANE_STORE"),
		SQLitePath:              os.Getenv("TOOL_CONTROL_PLANE_SQLITE_PATH"),
		CodeProvider:            os.Getenv("TOOL_CONTROL_PLANE_CODE_PROVIDER"),
		DeployProvider:          os.Getenv("TOOL_CONTROL_PLANE_DEPLOY_PROVIDER"),
		GitHubToken:             os.Getenv("GITHUB_TOKEN"),
		GitHubAppID:             os.Getenv("GITHUB_APP_ID"),
		GitHubAppInstallationID: os.Getenv("GITHUB_APP_INSTALLATION_ID"),
		GitHubAppPrivateKey:     normalizeGitHubAppPrivateKey(os.Getenv("GITHUB_APP_PRIVATE_KEY")),
		GitHubAppPrivateKeyPath: os.Getenv("GITHUB_APP_PRIVATE_KEY_PATH"),
		GitHubBaseURL:           os.Getenv("GITHUB_API_BASE_URL"),
		DemoRepository:          os.Getenv("TOOL_CONTROL_PLANE_DEMO_REPOSITORY"),
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
	return config, nil
}

func newServiceFromConfig(config Config) (*controlplane.Service, error) {
	registry := controlplane.DefaultCapabilityRegistry()
	adapters := controlplane.DefaultAdapterRegistry()
	store := controlplane.Store(controlplane.NewMemoryStore())
	if config.CodeProvider == controlplane.GitHubProvider || config.DeployProvider == controlplane.GitHubProvider {
		overrides := map[string]string{}
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
		registry = registry.WithProviderOverrides(overrides)
		tokenSource, err := githubTokenSourceFromConfig(config, http.DefaultClient)
		if err != nil {
			return nil, err
		}
		adapters = controlplane.DefaultAdapterRegistryWithGitHub(controlplane.GitHubAdapterConfig{
			Token:        config.GitHubToken,
			TokenSource:  tokenSource,
			BaseURL:      config.GitHubBaseURL,
			MaxAttempts:  config.GitHubMaxAttempts,
			RetryBackoff: config.GitHubRetryBackoff,
		})
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
		Adapters: adapters,
		Store:    store,
	}), nil
}

func newHandler(config Config, svc *controlplane.Service) http.Handler {
	return withRequestLogging(withRateLimit(withBearerAuth(newMux(svc, config), config.APIToken), newRateLimiter(config.RateLimitPerMinute, time.Minute)))
}

func envOrDefault(key string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
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
		(strings.TrimSpace(config.GitHubAppPrivateKey) != "" || strings.TrimSpace(config.GitHubAppPrivateKeyPath) != "")
}

func githubAppPrivateKey(config Config) (string, error) {
	if strings.TrimSpace(config.GitHubAppPrivateKey) != "" {
		return normalizeGitHubAppPrivateKey(config.GitHubAppPrivateKey), nil
	}
	path := strings.TrimSpace(config.GitHubAppPrivateKeyPath)
	if path == "" {
		return "", nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read GITHUB_APP_PRIVATE_KEY_PATH: %w", err)
	}
	return normalizeGitHubAppPrivateKey(string(content)), nil
}

func normalizeGitHubAppPrivateKey(value string) string {
	return strings.ReplaceAll(value, `\n`, "\n")
}
