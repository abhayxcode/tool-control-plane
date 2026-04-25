package controlplane

import (
	"path/filepath"
	"testing"
)

func TestSQLiteStorePersistsAuditAndApprovals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "controlplane.sqlite3")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}

	store.AppendAudit(AuditEntry{
		At:          "2026-07-09T00:00:00Z",
		OrgID:       "default",
		ActorUserID: "local-user",
		AgentRunID:  "run_123",
		ServiceID:   "backend",
		Environment: "prod",
		Capability:  "deploy",
		Action:      "rollback",
		RiskLevel:   RiskWriteHigh,
		Decision:    DecisionApprovalRequired,
	})
	approval := store.CreateApproval(ApprovalRequest{
		Status:      ApprovalPending,
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
		RiskLevel:   RiskWriteHigh,
		Reason:      "Tool action requires approval before execution.",
		RequestedAt: "2026-07-09T00:00:01Z",
	})
	approval.Status = ApprovalGranted
	approval.DecidedBy = "oncall-lead"
	if !store.UpdateApproval(approval) {
		t.Fatalf("expected approval update")
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close sqlite store: %v", err)
	}

	reopened, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("reopen sqlite store: %v", err)
	}
	defer reopened.Close()

	audit := reopened.Audit()
	if len(audit) != 1 {
		t.Fatalf("expected persisted audit entry, got %d", len(audit))
	}
	if audit[0].Decision != DecisionApprovalRequired {
		t.Fatalf("expected persisted audit decision")
	}

	storedApproval, ok := reopened.Approval("approval_000001")
	if !ok {
		t.Fatalf("expected persisted approval")
	}
	if storedApproval.Status != ApprovalGranted {
		t.Fatalf("expected granted approval, got %q", storedApproval.Status)
	}
	if storedApproval.Arguments["target_revision"] != "sha-abc123" {
		t.Fatalf("expected persisted approval arguments")
	}
	if storedApproval.DecidedBy != "oncall-lead" {
		t.Fatalf("expected persisted deciding actor")
	}
}

func TestSQLiteStoreContinuesApprovalIDsAfterReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "controlplane.sqlite3")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	first := store.CreateApproval(ApprovalRequest{Status: ApprovalPending})
	if err := store.Close(); err != nil {
		t.Fatalf("close sqlite store: %v", err)
	}

	reopened, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("reopen sqlite store: %v", err)
	}
	defer reopened.Close()
	second := reopened.CreateApproval(ApprovalRequest{Status: ApprovalPending})

	if first.ID != "approval_000001" {
		t.Fatalf("unexpected first ID: %q", first.ID)
	}
	if second.ID != "approval_000002" {
		t.Fatalf("expected sequence to continue, got %q", second.ID)
	}
}

func TestServiceCanUseSQLiteStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "controlplane.sqlite3")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	svc := NewServiceWithOptions(ServiceOptions{
		Store: store,
	})
	result := svc.CallTool(ToolCallRequest{
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
	if result.Status != DecisionApprovalRequired {
		t.Fatalf("expected approval required, got %q", result.Status)
	}
	if len(svc.Audit()) != 1 {
		t.Fatalf("expected audit entry")
	}
	if len(svc.Approvals()) != 1 {
		t.Fatalf("expected approval")
	}
}
