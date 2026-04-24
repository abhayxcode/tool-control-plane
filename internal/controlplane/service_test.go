package controlplane

import "testing"

func TestCallToolAllowsReadAction(t *testing.T) {
	svc := NewService()
	result := svc.CallTool(ToolCallRequest{
		OrgID:       "default",
		ActorUserID: "local-user",
		AgentRunID:  "run_123",
		ServiceID:   "backend",
		Environment: "prod",
		Capability:  "metrics",
		Action:      "get_service_health",
	})
	if result.Status != "success" {
		t.Fatalf("expected success, got %q", result.Status)
	}
	if result.RiskLevel != "read" {
		t.Fatalf("expected read risk, got %q", result.RiskLevel)
	}
	if len(svc.Audit()) != 1 {
		t.Fatalf("expected one audit entry")
	}
}

func TestCallToolDeniesUnknownAction(t *testing.T) {
	svc := NewService()
	result := svc.CallTool(ToolCallRequest{
		OrgID:       "default",
		ActorUserID: "local-user",
		AgentRunID:  "run_123",
		ServiceID:   "backend",
		Environment: "prod",
		Capability:  "deploy",
		Action:      "rollback",
	})
	if result.Status != "denied" {
		t.Fatalf("expected denied, got %q", result.Status)
	}
	audit := svc.Audit()
	if len(audit) != 1 {
		t.Fatalf("expected one audit entry")
	}
	if audit[0].Decision != "denied" {
		t.Fatalf("expected denied audit decision, got %q", audit[0].Decision)
	}
}
