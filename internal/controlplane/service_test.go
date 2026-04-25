package controlplane

import "testing"

func TestCapabilitiesExposeStableMetadata(t *testing.T) {
	svc := NewService()
	capabilities := svc.Capabilities()
	if len(capabilities) == 0 {
		t.Fatalf("expected capabilities")
	}
	if capabilities[0] != "ci.get_checks" {
		t.Fatalf("expected sorted capability IDs, got first ID %q", capabilities[0])
	}

	details := svc.CapabilityDetails()
	if len(details) != len(capabilities) {
		t.Fatalf("expected details for every capability")
	}

	var foundDraftPR bool
	for _, detail := range details {
		if detail.ID == "code_host.create_draft_pr" {
			foundDraftPR = true
			if detail.RiskLevel != RiskWriteLow {
				t.Fatalf("expected draft PR risk %q, got %q", RiskWriteLow, detail.RiskLevel)
			}
			if detail.Provider != "mock" {
				t.Fatalf("expected mock provider, got %q", detail.Provider)
			}
		}
	}
	if !foundDraftPR {
		t.Fatalf("expected draft PR capability metadata")
	}
}

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

func TestCallToolAllowsDraftPRWriteLowAction(t *testing.T) {
	svc := NewService()
	result := svc.CallTool(ToolCallRequest{
		OrgID:       "default",
		ActorUserID: "local-user",
		AgentRunID:  "run_123",
		ServiceID:   "backend",
		Environment: "prod",
		Capability:  "code_host",
		Action:      "create_draft_pr",
		Arguments: map[string]any{
			"title": "Draft: Revert backend database pool config",
		},
	})
	if result.Status != "success" {
		t.Fatalf("expected success, got %q", result.Status)
	}
	if result.RiskLevel != "write_low" {
		t.Fatalf("expected write_low risk, got %q", result.RiskLevel)
	}
	if result.Result["url"] == "" {
		t.Fatalf("expected PR URL")
	}
}

func TestCallToolAllowsCIReadAction(t *testing.T) {
	svc := NewService()
	result := svc.CallTool(ToolCallRequest{
		OrgID:       "default",
		ActorUserID: "local-user",
		AgentRunID:  "run_123",
		ServiceID:   "backend",
		Environment: "prod",
		Capability:  "ci",
		Action:      "get_checks",
		Arguments: map[string]any{
			"pr_number": 999,
		},
	})
	if result.Status != "success" {
		t.Fatalf("expected success, got %q", result.Status)
	}
	if result.RiskLevel != "read" {
		t.Fatalf("expected read risk, got %q", result.RiskLevel)
	}
	if result.Result["status"] != "passed" {
		t.Fatalf("expected passed CI status, got %#v", result.Result["status"])
	}
}
