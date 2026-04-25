package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
}
