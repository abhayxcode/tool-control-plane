package main

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/abhayxcode/tool-control-plane/api"
	"github.com/abhayxcode/tool-control-plane/internal/controlplane"
)

type requestIDContextKey struct{}

var requestSeq uint64

func main() {
	config, err := configFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	svc, err := newServiceFromConfig(config)
	if err != nil {
		log.Fatal(err)
	}
	handler := newHandler(config, svc)
	server := newHTTPServer(config, handler)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Printf("tool-control-plane listening on %s", config.Addr)
	if err := runHTTPServer(ctx, server, config.ShutdownTimeout); err != nil {
		log.Fatal(err)
	}
}

func newMux(svc *controlplane.Service, configs ...Config) *http.ServeMux {
	config := Config{}
	if len(configs) > 0 {
		config = configs[0]
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write(api.OpenAPISpec)
	})
	mux.HandleFunc("POST /mcp", mcpHandler(svc, config))
	mux.HandleFunc("GET /v1/readiness", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, readinessSummary(svc, config))
	})
	mux.HandleFunc("GET /v1/capabilities", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"capabilities":    svc.Capabilities(),
			"details":         svc.CapabilityDetails(),
			"provider_config": providerConfigSummary(config),
		})
	})
	mux.HandleFunc("GET /v1/connectors", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"connectors": connectorList(svc, config)})
	})
	mux.HandleFunc("POST /v1/connectors", func(w http.ResponseWriter, r *http.Request) {
		var req controlplane.ConnectorCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		connector, err := svc.CreateConnector(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSONStatus(w, http.StatusCreated, connector)
	})
	mux.HandleFunc("GET /v1/policies", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, policyPayload(svc, config))
	})
	mux.HandleFunc("POST /v1/tool-calls", func(w http.ResponseWriter, r *http.Request) {
		var req controlplane.ToolCallRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.RequestID == "" {
			req.RequestID = requestIDFromContext(r.Context())
		}
		writeJSON(w, svc.CallTool(req))
	})
	mux.HandleFunc("GET /v1/tool-calls", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"tool_calls": svc.ToolCalls()})
	})
	mux.HandleFunc("GET /v1/tool-calls/{id}", func(w http.ResponseWriter, r *http.Request) {
		record, ok := svc.ToolCall(r.PathValue("id"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, record)
	})
	mux.HandleFunc("GET /v1/audit", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"entries": svc.Audit()})
	})
	mux.HandleFunc("GET /v1/audit/export", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, auditExportPayload(svc))
	})
	mux.HandleFunc("GET /v1/approvals", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"approvals": svc.Approvals()})
	})
	mux.HandleFunc("GET /v1/approvals/{id}", func(w http.ResponseWriter, r *http.Request) {
		approval, ok := svc.Approval(r.PathValue("id"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, approval)
	})
	mux.HandleFunc("POST /v1/approvals/{id}/grant", func(w http.ResponseWriter, r *http.Request) {
		req, ok := decodeApprovalDecision(w, r)
		if !ok {
			return
		}
		result, found := svc.GrantApproval(r.PathValue("id"), req)
		if !found {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, result)
	})
	mux.HandleFunc("POST /v1/approvals/{id}/deny", func(w http.ResponseWriter, r *http.Request) {
		req, ok := decodeApprovalDecision(w, r)
		if !ok {
			return
		}
		result, found := svc.DenyApproval(r.PathValue("id"), req)
		if !found {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, result)
	})
	mux.HandleFunc("POST /v1/approvals/{id}/execute", func(w http.ResponseWriter, r *http.Request) {
		result, found := svc.ExecuteApproval(r.PathValue("id"))
		if !found {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, result)
	})
	return mux
}

func auditExportPayload(svc *controlplane.Service) map[string]any {
	return map[string]any{
		"schema_version": "2026-07-16.alpha1",
		"exported_at":    time.Now().UTC().Format(time.RFC3339),
		"audit":          svc.Audit(),
		"tool_calls":     svc.ToolCalls(),
		"approvals":      svc.Approvals(),
	}
}

func policyPayload(svc *controlplane.Service, config Config) map[string]any {
	source := "static"
	if strings.TrimSpace(config.PolicyFile) != "" {
		source = "file"
	}
	return map[string]any{
		"source":          source,
		"policy_file_set": strings.TrimSpace(config.PolicyFile) != "",
		"rule_count":      len(svc.PolicyRules()),
		"rules":           svc.PolicyRules(),
	}
}

func readinessSummary(svc *controlplane.Service, config Config) map[string]any {
	providerConfig := providerConfigSummary(config)
	blockers := providerConfigBlockers(config)
	repositoryAccess := repositoryAccessSummary(config)
	if repositoryAccessBlocker(repositoryAccess) != "" {
		blockers = append(blockers, repositoryAccessBlocker(repositoryAccess))
	}
	status := "ok"
	if len(blockers) > 0 {
		status = "blocked"
	}
	return map[string]any{
		"status":           status,
		"capability_count": len(svc.Capabilities()),
		"blockers":         blockers,
		"checks": map[string]any{
			"capability_registry_loaded": true,
			"provider_config":            providerConfig,
			"repository_access":          repositoryAccess,
			"store":                      providerStore(config),
			"auth_required":              strings.TrimSpace(config.APIToken) != "",
			"rate_limit_configured":      config.RateLimitPerMinute > 0,
			"policy":                     policyPayload(svc, config),
		},
	}
}

func providerConfigSummary(config Config) map[string]any {
	codeProvider := providerOrMock(config.CodeProvider)
	deployProvider := providerOrMock(config.DeployProvider)
	errorsProvider := providerOrMock(config.ErrorsProvider)
	metricsProvider := providerOrMock(config.MetricsProvider)
	runtimeProvider := providerOrMock(config.RuntimeProvider)
	docsProvider := providerOrMock(config.DocsProvider)
	internalAPIProvider := providerOrMock(config.InternalAPIProvider)
	githubSelected := codeProvider == controlplane.GitHubProvider || deployProvider == controlplane.GitHubProvider || docsProvider == controlplane.GitHubProvider
	githubTokenConfigured := strings.TrimSpace(config.GitHubToken) != ""
	githubAppAuthConfigured := githubAppConfigured(config)
	githubConfigured := githubTokenConfigured || githubAppAuthConfigured
	sentrySelected := errorsProvider == controlplane.SentryProvider
	sentryConfigured := strings.TrimSpace(config.SentryAuthToken) != ""
	prometheusSelected := metricsProvider == controlplane.PrometheusProvider
	prometheusConfigured := strings.TrimSpace(config.PrometheusBaseURL) != ""
	kubernetesSelected := runtimeProvider == controlplane.KubernetesProvider
	kubernetesConfigured := strings.TrimSpace(config.KubernetesBaseURL) != ""
	genericHTTPSelected := internalAPIProvider == controlplane.GenericHTTPProvider
	genericHTTPConfigured := strings.TrimSpace(config.GenericHTTPBaseURL) != ""
	return map[string]any{
		"code_provider":                   codeProvider,
		"deploy_provider":                 deployProvider,
		"errors_provider":                 errorsProvider,
		"metrics_provider":                metricsProvider,
		"runtime_provider":                runtimeProvider,
		"docs_provider":                   docsProvider,
		"internal_api_provider":           internalAPIProvider,
		"github_selected":                 githubSelected,
		"github_auth_mode":                githubAuthMode(config),
		"github_token_configured":         githubTokenConfigured,
		"github_app_configured":           githubAppAuthConfigured,
		"github_base_url_set":             strings.TrimSpace(config.GitHubBaseURL) != "",
		"github_max_attempts":             githubMaxAttempts(config),
		"github_retry_backoff_ms":         int(githubRetryBackoff(config) / time.Millisecond),
		"sentry_selected":                 sentrySelected,
		"sentry_token_configured":         sentryConfigured,
		"sentry_base_url_set":             strings.TrimSpace(config.SentryBaseURL) != "",
		"sentry_default_org_set":          strings.TrimSpace(config.SentryOrg) != "",
		"sentry_default_project_set":      strings.TrimSpace(config.SentryProject) != "",
		"prometheus_selected":             prometheusSelected,
		"prometheus_base_url_set":         prometheusConfigured,
		"prometheus_token_configured":     strings.TrimSpace(config.PrometheusBearerToken) != "",
		"prometheus_service_label":        firstConfiguredLabel(config.PrometheusServiceLabel, "service"),
		"prometheus_environment_label":    firstConfiguredLabel(config.PrometheusEnvLabel, "environment"),
		"prometheus_status_label":         firstConfiguredLabel(config.PrometheusStatusLabel, "status"),
		"kubernetes_selected":             kubernetesSelected,
		"kubernetes_base_url_set":         kubernetesConfigured,
		"kubernetes_token_configured":     strings.TrimSpace(config.KubernetesBearerToken) != "",
		"kubernetes_namespace":            firstConfiguredLabel(config.KubernetesNamespace, "default"),
		"kubernetes_label_selector":       strings.TrimSpace(config.KubernetesLabelSelector),
		"kubernetes_service_label":        firstConfiguredLabel(config.KubernetesServiceLabel, "app"),
		"kubernetes_environment_label":    strings.TrimSpace(config.KubernetesEnvLabel),
		"generic_http_selected":           genericHTTPSelected,
		"generic_http_base_url_set":       genericHTTPConfigured,
		"generic_http_token_configured":   strings.TrimSpace(config.GenericHTTPBearerToken) != "",
		"generic_http_allowed_methods":    genericHTTPAllowedMethods(config),
		"generic_http_timeout_ms":         int(genericHTTPTimeout(config) / time.Millisecond),
		"generic_http_max_response_bytes": genericHTTPMaxResponseBytes(config),
		"demo_repository":                 strings.TrimSpace(config.DemoRepository),
		"secret_broker":                   secretBrokerName(config),
		"store":                           providerStore(config),
		"ready":                           (!githubSelected || githubConfigured) && (!sentrySelected || sentryConfigured) && (!prometheusSelected || prometheusConfigured) && (!kubernetesSelected || kubernetesConfigured) && (!genericHTTPSelected || genericHTTPConfigured),
		"warnings":                        providerConfigBlockers(config),
	}
}

func providerConfigBlockers(config Config) []string {
	blockers := []string{}
	codeProvider := providerOrMock(config.CodeProvider)
	deployProvider := providerOrMock(config.DeployProvider)
	errorsProvider := providerOrMock(config.ErrorsProvider)
	metricsProvider := providerOrMock(config.MetricsProvider)
	runtimeProvider := providerOrMock(config.RuntimeProvider)
	docsProvider := providerOrMock(config.DocsProvider)
	internalAPIProvider := providerOrMock(config.InternalAPIProvider)
	githubSelected := codeProvider == controlplane.GitHubProvider || deployProvider == controlplane.GitHubProvider || docsProvider == controlplane.GitHubProvider
	if githubSelected && !githubCredentialConfigured(config) {
		blockers = append(blockers, "GITHUB_TOKEN or GitHub App installation credentials are required when a GitHub provider is selected.")
	}
	if errorsProvider == controlplane.SentryProvider && strings.TrimSpace(config.SentryAuthToken) == "" {
		blockers = append(blockers, "SENTRY_AUTH_TOKEN is required when the Sentry errors provider is selected.")
	}
	if metricsProvider == controlplane.PrometheusProvider && strings.TrimSpace(config.PrometheusBaseURL) == "" {
		blockers = append(blockers, "PROMETHEUS_BASE_URL is required when the Prometheus metrics provider is selected.")
	}
	if runtimeProvider == controlplane.KubernetesProvider && strings.TrimSpace(config.KubernetesBaseURL) == "" {
		blockers = append(blockers, "KUBERNETES_BASE_URL is required when the Kubernetes runtime provider is selected.")
	}
	if internalAPIProvider == controlplane.GenericHTTPProvider && strings.TrimSpace(config.GenericHTTPBaseURL) == "" {
		blockers = append(blockers, "GENERIC_HTTP_BASE_URL is required when the generic HTTP internal API provider is selected.")
	}
	return blockers
}

func repositoryAccessSummary(config Config) map[string]any {
	repository := strings.TrimSpace(config.DemoRepository)
	if repository == "" {
		return map[string]any{
			"status":     "skipped",
			"repository": "",
			"reason":     "TOOL_CONTROL_PLANE_DEMO_REPOSITORY is not set.",
		}
	}
	codeProvider := providerOrMock(config.CodeProvider)
	deployProvider := providerOrMock(config.DeployProvider)
	docsProvider := providerOrMock(config.DocsProvider)
	githubSelected := codeProvider == controlplane.GitHubProvider || deployProvider == controlplane.GitHubProvider || docsProvider == controlplane.GitHubProvider
	if !githubSelected {
		return map[string]any{
			"status":     "skipped",
			"repository": repository,
			"reason":     "GitHub provider is not selected.",
		}
	}
	if !githubCredentialConfigured(config) {
		return map[string]any{
			"status":     "blocked",
			"repository": repository,
			"reason":     "GITHUB_TOKEN or GitHub App installation credentials are required before repository access can be checked.",
		}
	}
	owner, repo, ok := splitRepository(repository)
	if !ok {
		return map[string]any{
			"status":     "blocked",
			"repository": repository,
			"reason":     "TOOL_CONTROL_PLANE_DEMO_REPOSITORY must be owner/repo.",
		}
	}
	status, reason := checkGitHubRepositoryAccess(config, owner, repo)
	return map[string]any{
		"status":     status,
		"repository": repository,
		"reason":     reason,
	}
}

func repositoryAccessBlocker(repositoryAccess map[string]any) string {
	if repositoryAccess["status"] == "blocked" {
		if reason, ok := repositoryAccess["reason"].(string); ok && strings.TrimSpace(reason) != "" {
			return reason
		}
		return "GitHub demo repository access check failed."
	}
	return ""
}

func splitRepository(repository string) (string, string, bool) {
	parts := strings.Split(repository, "/")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", false
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), true
}

func checkGitHubRepositoryAccess(config Config, owner string, repo string) (string, string) {
	baseURL := strings.TrimRight(strings.TrimSpace(config.GitHubBaseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	repoURL := baseURL + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo)
	client := &http.Client{Timeout: 2 * time.Second}
	tokenSource, err := githubTokenSourceFromConfig(config, client)
	if err != nil {
		return "blocked", err.Error()
	}
	token, err := tokenSource.Token()
	if err != nil {
		return "blocked", err.Error()
	}
	req, err := http.NewRequest(http.MethodGet, repoURL, nil)
	if err != nil {
		return "blocked", err.Error()
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return "blocked", err.Error()
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return "ok", ""
	}
	return "blocked", "GitHub repository access check returned HTTP " + strconv.Itoa(resp.StatusCode) + "."
}

func githubCredentialConfigured(config Config) bool {
	return strings.TrimSpace(config.GitHubToken) != "" || githubAppConfigured(config)
}

func githubAuthMode(config Config) string {
	if strings.TrimSpace(config.GitHubToken) != "" {
		return "token"
	}
	if githubAppConfigured(config) {
		return "github_app"
	}
	return "none"
}

func providerOrMock(provider string) string {
	if strings.TrimSpace(provider) == "" {
		return "mock"
	}
	return provider
}

func firstConfiguredLabel(value string, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func providerStore(config Config) string {
	if strings.TrimSpace(config.Store) != "" {
		return config.Store
	}
	if strings.TrimSpace(config.SQLitePath) != "" {
		return "sqlite"
	}
	return "memory"
}

func secretBrokerName(config Config) string {
	switch config.SecretBroker.(type) {
	case nil, controlplane.LocalSecretBroker:
		return "local"
	default:
		return "custom"
	}
}

func githubMaxAttempts(config Config) int {
	if config.GitHubMaxAttempts > 0 {
		return config.GitHubMaxAttempts
	}
	return 3
}

func githubRetryBackoff(config Config) time.Duration {
	if config.GitHubRetryBackoff > 0 {
		return config.GitHubRetryBackoff
	}
	return 200 * time.Millisecond
}

func genericHTTPAllowedMethods(config Config) []string {
	if len(config.GenericHTTPAllowedMethods) > 0 {
		return config.GenericHTTPAllowedMethods
	}
	return []string{http.MethodGet}
}

func genericHTTPTimeout(config Config) time.Duration {
	if config.GenericHTTPTimeout > 0 {
		return config.GenericHTTPTimeout
	}
	return 10 * time.Second
}

func genericHTTPMaxResponseBytes(config Config) int {
	if config.GenericHTTPMaxResponseBytes > 0 {
		return config.GenericHTTPMaxResponseBytes
	}
	return 64 * 1024
}

func withBearerAuth(next http.Handler, token string) http.Handler {
	token = strings.TrimSpace(token)
	if token == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type rateLimiter struct {
	mu        sync.Mutex
	limit     int
	window    time.Duration
	now       func() time.Time
	instances map[string]rateLimitInstance
}

type rateLimitInstance struct {
	windowStart time.Time
	count       int
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	if window <= 0 {
		window = time.Minute
	}
	return &rateLimiter{
		limit:     limit,
		window:    window,
		now:       time.Now,
		instances: map[string]rateLimitInstance{},
	}
}

func (l *rateLimiter) allow(key string) bool {
	if l == nil || l.limit <= 0 {
		return true
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	instance := l.instances[key]
	if instance.windowStart.IsZero() || now.Sub(instance.windowStart) >= l.window {
		instance = rateLimitInstance{windowStart: now}
	}
	if instance.count >= l.limit {
		l.instances[key] = instance
		return false
	}
	instance.count++
	l.instances[key] = instance
	return true
}

func withRateLimit(next http.Handler, limiter *rateLimiter) http.Handler {
	if limiter == nil || limiter.limit <= 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		if !limiter.allow(rateLimitKey(r)) {
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func rateLimitKey(r *http.Request) string {
	if authorization := strings.TrimSpace(r.Header.Get("Authorization")); authorization != "" {
		return authorization
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	return r.RemoteAddr
}

func withRequestLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := requestID(r)
		recorder := &statusRecorder{
			ResponseWriter: w,
			status:         http.StatusOK,
		}
		recorder.Header().Set("X-Request-ID", requestID)
		startedAt := time.Now()
		ctx := context.WithValue(r.Context(), requestIDContextKey{}, requestID)
		next.ServeHTTP(recorder, r.WithContext(ctx))
		logAccess(r, requestID, recorder.status, time.Since(startedAt))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func writeJSON(w http.ResponseWriter, value any) {
	writeJSONStatus(w, http.StatusOK, value)
}

func writeJSONStatus(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func requestID(r *http.Request) string {
	if value := strings.TrimSpace(r.Header.Get("X-Request-ID")); value != "" {
		return value
	}
	seq := atomic.AddUint64(&requestSeq, 1)
	return "req_" + time.Now().UTC().Format("20060102150405") + "_" + strconv.FormatUint(seq, 10)
}

func requestIDFromContext(ctx context.Context) string {
	value, _ := ctx.Value(requestIDContextKey{}).(string)
	return value
}

func logAccess(r *http.Request, requestID string, status int, duration time.Duration) {
	payload := map[string]any{
		"event":       "http_request",
		"request_id":  requestID,
		"method":      r.Method,
		"path":        r.URL.Path,
		"status":      status,
		"duration_ms": duration.Milliseconds(),
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		log.Printf("http_request request_id=%s method=%s path=%s status=%d duration_ms=%d", requestID, r.Method, r.URL.Path, status, duration.Milliseconds())
		return
	}
	log.Print(string(encoded))
}

func decodeApprovalDecision(w http.ResponseWriter, r *http.Request) (controlplane.ApprovalDecisionRequest, bool) {
	var req controlplane.ApprovalDecisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return req, false
	}
	return req, true
}
