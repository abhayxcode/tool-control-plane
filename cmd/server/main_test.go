package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/abhayxcode/tool-control-plane/internal/controlplane"
)

func TestApprovalHTTPFlow(t *testing.T) {
	mux := newMux(controlplane.NewService())

	toolBody := []byte(`{
		"org_id": "default",
		"actor_user_id": "local-user",
		"agent_run_id": "run_123",
		"service_id": "backend",
		"environment": "prod",
		"capability": "deploy",
		"action": "rollback",
		"arguments": {"target_revision": "sha-abc123"}
	}`)
	toolReq := httptest.NewRequest(http.MethodPost, "/v1/tool-calls", bytes.NewReader(toolBody))
	toolReq.Header.Set("Content-Type", "application/json")
	toolResp := httptest.NewRecorder()
	mux.ServeHTTP(toolResp, toolReq)

	if toolResp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", toolResp.Code)
	}
	var toolResult controlplane.ToolCallResponse
	if err := json.NewDecoder(toolResp.Body).Decode(&toolResult); err != nil {
		t.Fatalf("decode tool response: %v", err)
	}
	if toolResult.Status != controlplane.DecisionApprovalRequired {
		t.Fatalf("expected approval required, got %q", toolResult.Status)
	}
	if toolResult.ApprovalRequestID == "" {
		t.Fatalf("expected approval request ID")
	}

	listReq := httptest.NewRequest(http.MethodGet, "/v1/approvals", nil)
	listResp := httptest.NewRecorder()
	mux.ServeHTTP(listResp, listReq)
	if listResp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", listResp.Code)
	}
	var listResult struct {
		Approvals []controlplane.ApprovalRequest `json:"approvals"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&listResult); err != nil {
		t.Fatalf("decode approval list: %v", err)
	}
	if len(listResult.Approvals) != 1 {
		t.Fatalf("expected one approval, got %d", len(listResult.Approvals))
	}
	if listResult.Approvals[0].ID != toolResult.ApprovalRequestID {
		t.Fatalf("expected approval list to include tool approval")
	}

	grantBody := []byte(`{"actor_user_id": "oncall-lead", "reason": "Approved during incident."}`)
	grantReq := httptest.NewRequest(http.MethodPost, "/v1/approvals/"+toolResult.ApprovalRequestID+"/grant", bytes.NewReader(grantBody))
	grantReq.Header.Set("Content-Type", "application/json")
	grantResp := httptest.NewRecorder()
	mux.ServeHTTP(grantResp, grantReq)
	if grantResp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", grantResp.Code)
	}
	var grantResult controlplane.ApprovalDecisionResponse
	if err := json.NewDecoder(grantResp.Body).Decode(&grantResult); err != nil {
		t.Fatalf("decode grant response: %v", err)
	}
	if grantResult.Status != controlplane.ApprovalGranted {
		t.Fatalf("expected granted, got %q", grantResult.Status)
	}

	executeReq := httptest.NewRequest(http.MethodPost, "/v1/approvals/"+toolResult.ApprovalRequestID+"/execute", nil)
	executeResp := httptest.NewRecorder()
	mux.ServeHTTP(executeResp, executeReq)
	if executeResp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", executeResp.Code)
	}
	var executeResult controlplane.ApprovalExecuteResponse
	if err := json.NewDecoder(executeResp.Body).Decode(&executeResult); err != nil {
		t.Fatalf("decode execute response: %v", err)
	}
	if executeResult.Status != controlplane.DecisionApprovedExecuted {
		t.Fatalf("expected approved execution, got %q", executeResult.Status)
	}
	if executeResult.ToolCall.Result["rollback_id"] != "rollback-123" {
		t.Fatalf("expected rollback result")
	}
}

func TestNewServiceFromEnvCanRouteCodeCapabilitiesToGitHub(t *testing.T) {
	svc, err := newServiceFromConfig(Config{
		CodeProvider: controlplane.GitHubProvider,
		GitHubToken:  "test-token",
	})
	if err != nil {
		t.Fatalf("new service from config: %v", err)
	}
	var foundGitHubCI bool
	var foundMockMetrics bool
	for _, detail := range svc.CapabilityDetails() {
		if detail.ID == "ci.get_checks" && detail.Provider == controlplane.GitHubProvider {
			foundGitHubCI = true
		}
		if detail.ID == "metrics.get_service_health" && detail.Provider == "mock" {
			foundMockMetrics = true
		}
	}
	if !foundGitHubCI {
		t.Fatalf("expected ci.get_checks to use github provider")
	}
	if !foundMockMetrics {
		t.Fatalf("expected metrics capability to remain mock provider")
	}
}

func TestNewServiceFromEnvCanRouteDeployCapabilitiesToGitHub(t *testing.T) {
	svc, err := newServiceFromConfig(Config{
		DeployProvider: controlplane.GitHubProvider,
		GitHubToken:    "test-token",
	})
	if err != nil {
		t.Fatalf("new service from config: %v", err)
	}
	var foundGitHubDeploy bool
	var foundMockCodeHost bool
	for _, detail := range svc.CapabilityDetails() {
		if detail.ID == "deploy.get_recent_deploys" && detail.Provider == controlplane.GitHubProvider {
			foundGitHubDeploy = true
		}
		if detail.ID == "code_host.create_draft_pr" && detail.Provider == "mock" {
			foundMockCodeHost = true
		}
	}
	if !foundGitHubDeploy {
		t.Fatalf("expected deploy.get_recent_deploys to use github provider")
	}
	if !foundMockCodeHost {
		t.Fatalf("expected code_host.create_draft_pr to remain mock provider")
	}
}

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("TOOL_CONTROL_PLANE_ADDR", ":4200")
	t.Setenv("TOOL_CONTROL_PLANE_API_TOKEN", "secret-token")
	t.Setenv("TOOL_CONTROL_PLANE_RATE_LIMIT_PER_MINUTE", "12")
	t.Setenv("TOOL_CONTROL_PLANE_SHUTDOWN_TIMEOUT", "2s")
	t.Setenv("TOOL_CONTROL_PLANE_STORE", "sqlite")
	t.Setenv("TOOL_CONTROL_PLANE_SQLITE_PATH", "/tmp/controlplane.sqlite3")
	t.Setenv("TOOL_CONTROL_PLANE_CODE_PROVIDER", "github")
	t.Setenv("TOOL_CONTROL_PLANE_DEPLOY_PROVIDER", "github")
	t.Setenv("GITHUB_TOKEN", "github-token")
	t.Setenv("GITHUB_APP_ID", "12345")
	t.Setenv("GITHUB_APP_INSTALLATION_ID", "42")
	t.Setenv("GITHUB_APP_PRIVATE_KEY", "line1\\nline2")
	t.Setenv("GITHUB_API_BASE_URL", "https://github.example/api/v3")
	t.Setenv("TOOL_CONTROL_PLANE_GITHUB_MAX_ATTEMPTS", "4")
	t.Setenv("TOOL_CONTROL_PLANE_GITHUB_RETRY_BACKOFF", "25ms")
	t.Setenv("TOOL_CONTROL_PLANE_DEMO_REPOSITORY", "acme/backend")

	config, err := configFromEnv()
	if err != nil {
		t.Fatalf("config from env: %v", err)
	}
	if config.Addr != ":4200" {
		t.Fatalf("unexpected addr: %q", config.Addr)
	}
	if config.APIToken != "secret-token" {
		t.Fatalf("unexpected API token")
	}
	if config.RateLimitPerMinute != 12 {
		t.Fatalf("unexpected rate limit: %d", config.RateLimitPerMinute)
	}
	if config.ShutdownTimeout != 2*time.Second {
		t.Fatalf("unexpected shutdown timeout: %s", config.ShutdownTimeout)
	}
	if config.Store != "sqlite" || config.SQLitePath != "/tmp/controlplane.sqlite3" {
		t.Fatalf("unexpected store config")
	}
	if config.CodeProvider != "github" || config.DeployProvider != "github" || config.GitHubToken != "github-token" || config.GitHubBaseURL != "https://github.example/api/v3" || config.DemoRepository != "acme/backend" {
		t.Fatalf("unexpected GitHub config")
	}
	if config.GitHubAppID != "12345" || config.GitHubAppInstallationID != "42" || config.GitHubAppPrivateKey != "line1\nline2" {
		t.Fatalf("unexpected GitHub App config")
	}
	if config.GitHubMaxAttempts != 4 || config.GitHubRetryBackoff != 25*time.Millisecond {
		t.Fatalf("unexpected GitHub retry config")
	}
}

func TestConfigFromEnvRejectsInvalidRateLimit(t *testing.T) {
	t.Setenv("TOOL_CONTROL_PLANE_RATE_LIMIT_PER_MINUTE", "nope")
	_, err := configFromEnv()
	if err == nil {
		t.Fatalf("expected invalid rate limit error")
	}
}

func TestConfigFromEnvRejectsInvalidShutdownTimeout(t *testing.T) {
	t.Setenv("TOOL_CONTROL_PLANE_SHUTDOWN_TIMEOUT", "nope")
	_, err := configFromEnv()
	if err == nil {
		t.Fatalf("expected invalid shutdown timeout error")
	}
}

func TestConfigFromEnvRejectsInvalidGitHubRetryConfig(t *testing.T) {
	t.Setenv("TOOL_CONTROL_PLANE_GITHUB_MAX_ATTEMPTS", "0")
	if _, err := configFromEnv(); err == nil {
		t.Fatalf("expected invalid GitHub max attempts error")
	}

	t.Setenv("TOOL_CONTROL_PLANE_GITHUB_MAX_ATTEMPTS", "2")
	t.Setenv("TOOL_CONTROL_PLANE_GITHUB_RETRY_BACKOFF", "nope")
	if _, err := configFromEnv(); err == nil {
		t.Fatalf("expected invalid GitHub retry backoff error")
	}
}

func TestCapabilitiesExposeProviderConfigReadiness(t *testing.T) {
	svc, err := newServiceFromConfig(Config{
		CodeProvider:   controlplane.GitHubProvider,
		DeployProvider: controlplane.GitHubProvider,
		GitHubToken:    "test-token",
		Store:          "sqlite",
	})
	if err != nil {
		t.Fatalf("new service from config: %v", err)
	}
	mux := newMux(svc, Config{
		CodeProvider:   controlplane.GitHubProvider,
		DeployProvider: controlplane.GitHubProvider,
		GitHubToken:    "test-token",
		Store:          "sqlite",
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil)
	resp := httptest.NewRecorder()

	mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}
	var body struct {
		ProviderConfig map[string]any `json:"provider_config"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}
	if body.ProviderConfig["code_provider"] != controlplane.GitHubProvider {
		t.Fatalf("expected github code provider, got %#v", body.ProviderConfig["code_provider"])
	}
	if body.ProviderConfig["deploy_provider"] != controlplane.GitHubProvider {
		t.Fatalf("expected github deploy provider, got %#v", body.ProviderConfig["deploy_provider"])
	}
	if body.ProviderConfig["github_token_configured"] != true {
		t.Fatalf("expected configured github token")
	}
	if body.ProviderConfig["github_auth_mode"] != "token" {
		t.Fatalf("expected token auth mode, got %#v", body.ProviderConfig["github_auth_mode"])
	}
	if body.ProviderConfig["ready"] != true {
		t.Fatalf("expected provider config ready")
	}
	if body.ProviderConfig["store"] != "sqlite" {
		t.Fatalf("expected sqlite store, got %#v", body.ProviderConfig["store"])
	}
}

func TestCapabilitiesExposeGitHubAppProviderConfigReadiness(t *testing.T) {
	mux := newMux(controlplane.NewService(), Config{
		CodeProvider:            controlplane.GitHubProvider,
		GitHubAppID:             "12345",
		GitHubAppInstallationID: "42",
		GitHubAppPrivateKey:     "present",
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil)
	resp := httptest.NewRecorder()

	mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}
	var body struct {
		ProviderConfig map[string]any `json:"provider_config"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}
	if body.ProviderConfig["github_auth_mode"] != "github_app" {
		t.Fatalf("expected github_app auth mode, got %#v", body.ProviderConfig["github_auth_mode"])
	}
	if body.ProviderConfig["github_app_configured"] != true {
		t.Fatalf("expected github app configured")
	}
	if body.ProviderConfig["github_token_configured"] != false {
		t.Fatalf("expected no static github token")
	}
	if body.ProviderConfig["ready"] != true {
		t.Fatalf("expected provider config ready")
	}
}

func TestCapabilitiesExposeMissingGitHubTokenWarning(t *testing.T) {
	mux := newMux(controlplane.NewService(), Config{
		CodeProvider: controlplane.GitHubProvider,
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil)
	resp := httptest.NewRecorder()

	mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}
	var body struct {
		ProviderConfig struct {
			Ready    bool     `json:"ready"`
			Warnings []string `json:"warnings"`
		} `json:"provider_config"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}
	if body.ProviderConfig.Ready {
		t.Fatalf("expected provider config not ready")
	}
	if len(body.ProviderConfig.Warnings) != 1 {
		t.Fatalf("expected one warning, got %d", len(body.ProviderConfig.Warnings))
	}
}

func TestReadinessReportsProviderBlockers(t *testing.T) {
	mux := newMux(controlplane.NewService(), Config{
		CodeProvider: controlplane.GitHubProvider,
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/readiness", nil)
	resp := httptest.NewRecorder()

	mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}
	var body struct {
		Status          string   `json:"status"`
		CapabilityCount int      `json:"capability_count"`
		Blockers        []string `json:"blockers"`
		Checks          struct {
			ProviderConfig map[string]any `json:"provider_config"`
		} `json:"checks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode readiness: %v", err)
	}
	if body.Status != "blocked" {
		t.Fatalf("expected blocked readiness, got %q", body.Status)
	}
	if body.CapabilityCount == 0 {
		t.Fatalf("expected capability count")
	}
	if len(body.Blockers) != 1 {
		t.Fatalf("expected one blocker, got %d", len(body.Blockers))
	}
	if body.Checks.ProviderConfig["ready"] != false {
		t.Fatalf("expected provider config not ready")
	}
}

func TestReadinessReportsReadyProviderConfig(t *testing.T) {
	github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/backend" {
			t.Fatalf("unexpected GitHub path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("expected bearer token")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"full_name":"acme/backend"}`))
	}))
	defer github.Close()

	mux := newMux(controlplane.NewService(), Config{
		CodeProvider:   controlplane.GitHubProvider,
		DeployProvider: controlplane.GitHubProvider,
		GitHubToken:    "test-token",
		GitHubBaseURL:  github.URL,
		DemoRepository: "acme/backend",
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/readiness", nil)
	resp := httptest.NewRecorder()

	mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}
	var body struct {
		Status   string   `json:"status"`
		Blockers []string `json:"blockers"`
		Checks   struct {
			ProviderConfig   map[string]any `json:"provider_config"`
			RepositoryAccess map[string]any `json:"repository_access"`
		} `json:"checks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode readiness: %v", err)
	}
	if body.Status != "ok" {
		t.Fatalf("expected ok readiness, got %q", body.Status)
	}
	if len(body.Blockers) != 0 {
		t.Fatalf("expected no blockers, got %d", len(body.Blockers))
	}
	if body.Checks.ProviderConfig["github_token_configured"] != true {
		t.Fatalf("expected configured github token")
	}
	if body.Checks.ProviderConfig["github_max_attempts"] != float64(3) {
		t.Fatalf("expected default github max attempts, got %#v", body.Checks.ProviderConfig["github_max_attempts"])
	}
	if body.Checks.ProviderConfig["github_retry_backoff_ms"] != float64(200) {
		t.Fatalf("expected default github retry backoff, got %#v", body.Checks.ProviderConfig["github_retry_backoff_ms"])
	}
	if body.Checks.RepositoryAccess["status"] != "ok" {
		t.Fatalf("expected repository access ok, got %#v", body.Checks.RepositoryAccess)
	}
}

func TestReadinessUsesGitHubAppForRepositoryAccess(t *testing.T) {
	privateKey := testGitHubAppPrivateKeyPEM(t)
	var tokenRequests int
	var repositoryRequests int
	github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && r.URL.Path == "/app/installations/42/access_tokens" {
			tokenRequests++
			if r.Header.Get("Authorization") == "" {
				t.Fatalf("expected github app JWT authorization")
			}
			w.Write([]byte(`{"token":"installation-token","expires_at":"2026-07-16T13:00:00Z"}`))
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/repos/acme/backend" {
			repositoryRequests++
			if r.Header.Get("Authorization") != "Bearer installation-token" {
				t.Fatalf("expected installation token, got %q", r.Header.Get("Authorization"))
			}
			w.Write([]byte(`{"full_name":"acme/backend"}`))
			return
		}
		t.Fatalf("unexpected GitHub path: %s %s", r.Method, r.URL.Path)
	}))
	defer github.Close()

	mux := newMux(controlplane.NewService(), Config{
		CodeProvider:            controlplane.GitHubProvider,
		GitHubAppID:             "12345",
		GitHubAppInstallationID: "42",
		GitHubAppPrivateKey:     privateKey,
		GitHubBaseURL:           github.URL,
		DemoRepository:          "acme/backend",
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/readiness", nil)
	resp := httptest.NewRecorder()

	mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}
	var body struct {
		Status string `json:"status"`
		Checks struct {
			ProviderConfig   map[string]any `json:"provider_config"`
			RepositoryAccess map[string]any `json:"repository_access"`
		} `json:"checks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode readiness: %v", err)
	}
	if body.Status != "ok" {
		t.Fatalf("expected ok readiness, got %q", body.Status)
	}
	if body.Checks.ProviderConfig["github_auth_mode"] != "github_app" {
		t.Fatalf("expected github app auth mode, got %#v", body.Checks.ProviderConfig["github_auth_mode"])
	}
	if body.Checks.RepositoryAccess["status"] != "ok" {
		t.Fatalf("expected repository access ok, got %#v", body.Checks.RepositoryAccess)
	}
	if tokenRequests != 1 || repositoryRequests != 1 {
		t.Fatalf("expected one token and one repository request, got %d and %d", tokenRequests, repositoryRequests)
	}
}

func TestReadinessBlocksWhenDemoRepositoryIsInaccessible(t *testing.T) {
	github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer github.Close()

	mux := newMux(controlplane.NewService(), Config{
		CodeProvider:   controlplane.GitHubProvider,
		DeployProvider: controlplane.GitHubProvider,
		GitHubToken:    "test-token",
		GitHubBaseURL:  github.URL,
		DemoRepository: "acme/backend",
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/readiness", nil)
	resp := httptest.NewRecorder()

	mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}
	var body struct {
		Status   string   `json:"status"`
		Blockers []string `json:"blockers"`
		Checks   struct {
			RepositoryAccess map[string]any `json:"repository_access"`
		} `json:"checks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode readiness: %v", err)
	}
	if body.Status != "blocked" {
		t.Fatalf("expected blocked readiness, got %q", body.Status)
	}
	if len(body.Blockers) != 1 {
		t.Fatalf("expected one blocker, got %d", len(body.Blockers))
	}
	if body.Checks.RepositoryAccess["status"] != "blocked" {
		t.Fatalf("expected blocked repository access")
	}
}

func TestRunHTTPServerShutsDownWhenContextCancels(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	server := newHTTPServer(Config{Addr: listener.Addr().String()}, newMux(controlplane.NewService()))

	done := make(chan error, 1)
	go func() {
		done <- runHTTPServerOnListener(ctx, server, listener, time.Second)
	}()
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected graceful shutdown, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("server did not shut down")
	}
}

func TestBearerAuthProtectsAPIWhenTokenConfigured(t *testing.T) {
	handler := withBearerAuth(newMux(controlplane.NewService()), "secret-token")

	unauthorizedReq := httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil)
	unauthorizedResp := httptest.NewRecorder()
	handler.ServeHTTP(unauthorizedResp, unauthorizedReq)
	if unauthorizedResp.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", unauthorizedResp.Code)
	}

	authorizedReq := httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil)
	authorizedReq.Header.Set("Authorization", "Bearer secret-token")
	authorizedResp := httptest.NewRecorder()
	handler.ServeHTTP(authorizedResp, authorizedReq)
	if authorizedResp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", authorizedResp.Code)
	}
}

func TestBearerAuthAllowsHealthWithoutToken(t *testing.T) {
	handler := withBearerAuth(newMux(controlplane.NewService()), "secret-token")
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	resp := httptest.NewRecorder()

	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected health bypass, got %d", resp.Code)
	}
}

func TestBearerAuthDisabledWhenTokenEmpty(t *testing.T) {
	handler := withBearerAuth(newMux(controlplane.NewService()), "")
	req := httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil)
	resp := httptest.NewRecorder()

	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected open local dev mode, got %d", resp.Code)
	}
}

func TestRequestLoggingAddsRequestIDHeader(t *testing.T) {
	handler := withRequestLogging(newMux(controlplane.NewService()))
	req := httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil)
	req.Header.Set("X-Request-ID", "req-test-123")
	resp := httptest.NewRecorder()

	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}
	if resp.Header().Get("X-Request-ID") != "req-test-123" {
		t.Fatalf("expected response request ID header")
	}
}

func TestRequestIDPropagatesToToolAudit(t *testing.T) {
	svc := controlplane.NewService()
	handler := withRequestLogging(newMux(svc))
	body := []byte(`{
		"org_id": "default",
		"actor_user_id": "local-user",
		"agent_run_id": "run_123",
		"service_id": "backend",
		"environment": "prod",
		"capability": "metrics",
		"action": "get_service_health"
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/tool-calls", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", "req-tool-123")
	resp := httptest.NewRecorder()

	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}
	audit := svc.Audit()
	if len(audit) != 1 {
		t.Fatalf("expected one audit entry")
	}
	if audit[0].RequestID != "req-tool-123" {
		t.Fatalf("expected audit request ID, got %q", audit[0].RequestID)
	}
}

func TestRateLimitBlocksAfterLimit(t *testing.T) {
	limiter := newRateLimiter(1, time.Minute)
	handler := withRateLimit(newMux(controlplane.NewService()), limiter)

	firstReq := httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil)
	firstReq.RemoteAddr = "192.0.2.1:1234"
	firstResp := httptest.NewRecorder()
	handler.ServeHTTP(firstResp, firstReq)
	if firstResp.Code != http.StatusOK {
		t.Fatalf("expected first request allowed, got %d", firstResp.Code)
	}

	secondReq := httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil)
	secondReq.RemoteAddr = "192.0.2.1:1235"
	secondResp := httptest.NewRecorder()
	handler.ServeHTTP(secondResp, secondReq)
	if secondResp.Code != http.StatusTooManyRequests {
		t.Fatalf("expected second request rate limited, got %d", secondResp.Code)
	}
}

func TestRateLimitUsesBearerTokenAsKey(t *testing.T) {
	limiter := newRateLimiter(1, time.Minute)
	handler := withRateLimit(newMux(controlplane.NewService()), limiter)

	firstReq := httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil)
	firstReq.RemoteAddr = "192.0.2.1:1234"
	firstReq.Header.Set("Authorization", "Bearer token-a")
	firstResp := httptest.NewRecorder()
	handler.ServeHTTP(firstResp, firstReq)
	if firstResp.Code != http.StatusOK {
		t.Fatalf("expected first token request allowed, got %d", firstResp.Code)
	}

	secondReq := httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil)
	secondReq.RemoteAddr = "192.0.2.1:1234"
	secondReq.Header.Set("Authorization", "Bearer token-b")
	secondResp := httptest.NewRecorder()
	handler.ServeHTTP(secondResp, secondReq)
	if secondResp.Code != http.StatusOK {
		t.Fatalf("expected different token request allowed, got %d", secondResp.Code)
	}
}

func TestRateLimitResetsAfterWindow(t *testing.T) {
	now := time.Date(2026, 7, 9, 0, 0, 0, 0, time.UTC)
	limiter := newRateLimiter(1, time.Minute)
	limiter.now = func() time.Time { return now }
	handler := withRateLimit(newMux(controlplane.NewService()), limiter)

	firstReq := httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil)
	firstReq.RemoteAddr = "192.0.2.1:1234"
	firstResp := httptest.NewRecorder()
	handler.ServeHTTP(firstResp, firstReq)
	if firstResp.Code != http.StatusOK {
		t.Fatalf("expected first request allowed, got %d", firstResp.Code)
	}

	now = now.Add(time.Minute)
	secondReq := httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil)
	secondReq.RemoteAddr = "192.0.2.1:1235"
	secondResp := httptest.NewRecorder()
	handler.ServeHTTP(secondResp, secondReq)
	if secondResp.Code != http.StatusOK {
		t.Fatalf("expected request allowed after reset, got %d", secondResp.Code)
	}
}

func TestRateLimitAllowsHealth(t *testing.T) {
	limiter := newRateLimiter(0, time.Minute)
	handler := withRateLimit(newMux(controlplane.NewService()), limiter)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	resp := httptest.NewRecorder()

	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected health allowed, got %d", resp.Code)
	}
}

func testGitHubAppPrivateKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}))
}
