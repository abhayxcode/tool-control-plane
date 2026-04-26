package main

import (
	"bytes"
	"encoding/json"
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

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("TOOL_CONTROL_PLANE_ADDR", ":4200")
	t.Setenv("TOOL_CONTROL_PLANE_API_TOKEN", "secret-token")
	t.Setenv("TOOL_CONTROL_PLANE_RATE_LIMIT_PER_MINUTE", "12")
	t.Setenv("TOOL_CONTROL_PLANE_STORE", "sqlite")
	t.Setenv("TOOL_CONTROL_PLANE_SQLITE_PATH", "/tmp/controlplane.sqlite3")
	t.Setenv("TOOL_CONTROL_PLANE_CODE_PROVIDER", "github")
	t.Setenv("GITHUB_TOKEN", "github-token")
	t.Setenv("GITHUB_API_BASE_URL", "https://github.example/api/v3")

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
	if config.Store != "sqlite" || config.SQLitePath != "/tmp/controlplane.sqlite3" {
		t.Fatalf("unexpected store config")
	}
	if config.CodeProvider != "github" || config.GitHubToken != "github-token" || config.GitHubBaseURL != "https://github.example/api/v3" {
		t.Fatalf("unexpected GitHub config")
	}
}

func TestConfigFromEnvRejectsInvalidRateLimit(t *testing.T) {
	t.Setenv("TOOL_CONTROL_PLANE_RATE_LIMIT_PER_MINUTE", "nope")
	_, err := configFromEnv()
	if err == nil {
		t.Fatalf("expected invalid rate limit error")
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
