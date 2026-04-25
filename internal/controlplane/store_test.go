package controlplane

import "testing"

func TestMemoryStoreCreatesApprovalsInOrder(t *testing.T) {
	store := NewMemoryStore()
	first := store.CreateApproval(ApprovalRequest{
		Status: ApprovalPending,
		OrgID:  "default",
	})
	second := store.CreateApproval(ApprovalRequest{
		Status: ApprovalPending,
		OrgID:  "default",
	})

	if first.ID != "approval_000001" {
		t.Fatalf("unexpected first approval ID: %q", first.ID)
	}
	if second.ID != "approval_000002" {
		t.Fatalf("unexpected second approval ID: %q", second.ID)
	}

	approvals := store.Approvals()
	if len(approvals) != 2 {
		t.Fatalf("expected two approvals")
	}
	if approvals[0].ID != first.ID || approvals[1].ID != second.ID {
		t.Fatalf("expected approval insertion order")
	}
}

func TestMemoryStoreUpdatesApproval(t *testing.T) {
	store := NewMemoryStore()
	approval := store.CreateApproval(ApprovalRequest{
		Status: ApprovalPending,
	})
	approval.Status = ApprovalGranted

	if !store.UpdateApproval(approval) {
		t.Fatalf("expected update success")
	}
	stored, ok := store.Approval(approval.ID)
	if !ok {
		t.Fatalf("expected stored approval")
	}
	if stored.Status != ApprovalGranted {
		t.Fatalf("expected granted status, got %q", stored.Status)
	}
	if store.UpdateApproval(ApprovalRequest{ID: "missing"}) {
		t.Fatalf("expected missing approval update to fail")
	}
}

func TestMemoryStoreReturnsAuditCopy(t *testing.T) {
	store := NewMemoryStore()
	store.AppendAudit(AuditEntry{
		Decision: DecisionAllowed,
	})

	audit := store.Audit()
	audit[0].Decision = DecisionDenied

	stored := store.Audit()
	if stored[0].Decision != DecisionAllowed {
		t.Fatalf("expected audit copy")
	}
}
