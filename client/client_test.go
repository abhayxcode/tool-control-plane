package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientCallsToolAndApprovalLifecycle(t *testing.T) {
	testServer := httptest.NewServer(testMux())
	defer testServer.Close()

	tcp, err := New(testServer.URL, WithHTTPClient(testServer.Client()))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	ctx := context.Background()

	health, err := tcp.Health(ctx)
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if health["status"] != "ok" {
		t.Fatalf("expected healthy response")
	}

	capabilities, details, err := tcp.Capabilities(ctx)
	if err != nil {
		t.Fatalf("capabilities: %v", err)
	}
	if len(capabilities) == 0 || len(details) == 0 {
		t.Fatalf("expected capabilities")
	}

	toolCall, err := tcp.CallTool(ctx, ToolCallRequest{
		OrgID:       "default",
		ActorUserID: "local-user",
		AgentRunID:  "run_123",
		ServiceID:   "backend",
		Environment: "prod",
		Capability:  "deploy",
		Action:      "rollback",
		Arguments: map[string]any{
			"target_revision": "sha-abc123",
		},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if toolCall.Status != "approval_required" {
		t.Fatalf("expected approval required, got %q", toolCall.Status)
	}

	approval, err := tcp.Approval(ctx, toolCall.ApprovalRequestID)
	if err != nil {
		t.Fatalf("get approval: %v", err)
	}
	if approval.Status != "pending" {
		t.Fatalf("expected pending approval")
	}

	grant, err := tcp.GrantApproval(ctx, toolCall.ApprovalRequestID, ApprovalDecisionRequest{
		ActorUserID: "oncall-lead",
		Reason:      "Approved during incident.",
	})
	if err != nil {
		t.Fatalf("grant approval: %v", err)
	}
	if grant.Status != "granted" {
		t.Fatalf("expected granted approval")
	}

	executed, err := tcp.ExecuteApproval(ctx, toolCall.ApprovalRequestID)
	if err != nil {
		t.Fatalf("execute approval: %v", err)
	}
	if executed.Status != "approved_executed" {
		t.Fatalf("expected approved execution, got %q", executed.Status)
	}

	audit, err := tcp.Audit(ctx)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if len(audit) != 2 {
		t.Fatalf("expected two audit entries, got %d", len(audit))
	}

	toolCalls, err := tcp.ToolCalls(ctx)
	if err != nil {
		t.Fatalf("tool calls: %v", err)
	}
	if len(toolCalls) != 1 {
		t.Fatalf("expected one tool call record, got %d", len(toolCalls))
	}
	if toolCalls[0].Arguments["token"] != "[redacted]" {
		t.Fatalf("expected redacted token")
	}
	toolRecord, err := tcp.ToolCall(ctx, toolCalls[0].ID)
	if err != nil {
		t.Fatalf("tool call: %v", err)
	}
	if toolRecord.ID != toolCalls[0].ID {
		t.Fatalf("expected matching tool call record")
	}

	export, err := tcp.AuditExport(ctx)
	if err != nil {
		t.Fatalf("audit export: %v", err)
	}
	if export.SchemaVersion == "" || len(export.Audit) != 2 || len(export.ToolCalls) != 1 || len(export.Approvals) != 1 {
		t.Fatalf("unexpected audit export: %#v", export)
	}
}

func TestClientFetchesOpenAPI(t *testing.T) {
	testServer := httptest.NewServer(testMux())
	defer testServer.Close()

	tcp, err := New(testServer.URL, WithHTTPClient(testServer.Client()))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	spec, err := tcp.OpenAPI(context.Background())
	if err != nil {
		t.Fatalf("openapi: %v", err)
	}
	if !strings.Contains(string(spec), "openapi: 3.1.0") {
		t.Fatalf("expected OpenAPI YAML")
	}
}

func TestClientReturnsHTTPError(t *testing.T) {
	testServer := httptest.NewServer(testMux())
	defer testServer.Close()

	tcp, err := New(testServer.URL, WithHTTPClient(testServer.Client()))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = tcp.Approval(context.Background(), "missing")
	if err == nil {
		t.Fatalf("expected missing approval error")
	}
	if !strings.Contains(err.Error(), "status 404") {
		t.Fatalf("expected status code in error, got %v", err)
	}
}

func TestClientSendsBearerToken(t *testing.T) {
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		writeTestJSON(w, map[string]string{"status": "ok"})
	}))
	defer testServer.Close()

	tcp, err := New(testServer.URL, WithHTTPClient(testServer.Client()), WithBearerToken("secret-token"))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	health, err := tcp.Health(context.Background())
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if health["status"] != "ok" {
		t.Fatalf("expected healthy response")
	}
}

func testMux() *http.ServeMux {
	mux := http.NewServeMux()
	approval := ApprovalRequest{
		ID:          "approval_000001",
		Status:      "pending",
		OrgID:       "default",
		ActorUserID: "local-user",
		AgentRunID:  "run_123",
		ServiceID:   "backend",
		Environment: "prod",
		Capability:  "deploy",
		Action:      "rollback",
		RiskLevel:   "write_high",
		Reason:      "Tool action requires approval before execution.",
		RequestedAt: "2026-07-09T00:00:00Z",
	}
	toolCallRecord := ToolCallRecord{
		ID:          "tool_call_000001",
		At:          "2026-07-09T00:00:00Z",
		RequestID:   "req-tool-123",
		OrgID:       "default",
		ActorUserID: "local-user",
		AgentRunID:  "run_123",
		ServiceID:   "backend",
		Environment: "prod",
		Capability:  "deploy",
		Action:      "rollback",
		Arguments: map[string]any{
			"target_revision": "sha-abc123",
			"token":           "[redacted]",
		},
		RiskLevel:         "write_high",
		Decision:          "approval_required",
		Status:            "approval_required",
		Reason:            "Tool action requires approval before execution.",
		ApprovalRequestID: approval.ID,
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(w, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /v1/capabilities", func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(w, map[string]any{
			"capabilities": []string{"deploy.rollback"},
			"details": []CapabilityDefinition{
				{
					ID:               "deploy.rollback",
					Capability:       "deploy",
					Action:           "rollback",
					RiskLevel:        "write_high",
					Provider:         "mock",
					Description:      "Rollback a deployment.",
					ApprovalRequired: true,
				},
			},
		})
	})
	mux.HandleFunc("POST /v1/tool-calls", func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(w, ToolCallResponse{
			Status:            "approval_required",
			RiskLevel:         "write_high",
			Reason:            "Tool action requires approval before execution.",
			ApprovalRequired:  true,
			ApprovalRequestID: approval.ID,
		})
	})
	mux.HandleFunc("GET /v1/tool-calls", func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(w, map[string]any{
			"tool_calls": []ToolCallRecord{toolCallRecord},
		})
	})
	mux.HandleFunc("GET /v1/tool-calls/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != toolCallRecord.ID {
			http.NotFound(w, r)
			return
		}
		writeTestJSON(w, toolCallRecord)
	})
	mux.HandleFunc("GET /v1/approvals/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != approval.ID {
			http.NotFound(w, r)
			return
		}
		writeTestJSON(w, approval)
	})
	mux.HandleFunc("POST /v1/approvals/{id}/grant", func(w http.ResponseWriter, r *http.Request) {
		approval.Status = "granted"
		writeTestJSON(w, ApprovalDecisionResponse{
			Status:   approval.Status,
			Approval: approval,
		})
	})
	mux.HandleFunc("POST /v1/approvals/{id}/execute", func(w http.ResponseWriter, r *http.Request) {
		approval.Executed = true
		writeTestJSON(w, ApprovalExecuteResponse{
			Status:   "approved_executed",
			Approval: approval,
			ToolCall: ToolCallResponse{
				Status:    "success",
				RiskLevel: "write_high",
				Provider:  "mock",
				Result: map[string]any{
					"rollback_id": "rollback-123",
				},
			},
		})
	})
	mux.HandleFunc("GET /v1/audit", func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(w, map[string]any{
			"entries": []AuditEntry{
				{Decision: "approval_required"},
				{Decision: "approved_executed"},
			},
		})
	})
	mux.HandleFunc("GET /v1/audit/export", func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(w, AuditExportResponse{
			SchemaVersion: "2026-07-16.alpha1",
			ExportedAt:    "2026-07-16T00:00:00Z",
			Audit: []AuditEntry{
				{Decision: "approval_required"},
				{Decision: "approved_executed"},
			},
			ToolCalls: []ToolCallRecord{toolCallRecord},
			Approvals: []ApprovalRequest{approval},
		})
	})
	mux.HandleFunc("GET /openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		w.Write([]byte("openapi: 3.1.0\n"))
	})
	return mux
}

func writeTestJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(value)
}
