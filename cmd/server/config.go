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
	Addr               string
	ShutdownTimeout    time.Duration
	APIToken           string
	RateLimitPerMinute int
	Store              string
	SQLitePath         string
	CodeProvider       string
	DeployProvider     string
	GitHubToken        string
	GitHubBaseURL      string
}

func configFromEnv() (Config, error) {
	config := Config{
		Addr:            envOrDefault("TOOL_CONTROL_PLANE_ADDR", ":4100"),
		ShutdownTimeout: 10 * time.Second,
		APIToken:        os.Getenv("TOOL_CONTROL_PLANE_API_TOKEN"),
		Store:           os.Getenv("TOOL_CONTROL_PLANE_STORE"),
		SQLitePath:      os.Getenv("TOOL_CONTROL_PLANE_SQLITE_PATH"),
		CodeProvider:    os.Getenv("TOOL_CONTROL_PLANE_CODE_PROVIDER"),
		DeployProvider:  os.Getenv("TOOL_CONTROL_PLANE_DEPLOY_PROVIDER"),
		GitHubToken:     os.Getenv("GITHUB_TOKEN"),
		GitHubBaseURL:   os.Getenv("GITHUB_API_BASE_URL"),
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
		adapters = controlplane.DefaultAdapterRegistryWithGitHub(controlplane.GitHubAdapterConfig{
			Token:   config.GitHubToken,
			BaseURL: config.GitHubBaseURL,
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
	return withRequestLogging(withRateLimit(withBearerAuth(newMux(svc), config.APIToken), newRateLimiter(config.RateLimitPerMinute, time.Minute)))
}

func envOrDefault(key string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}
