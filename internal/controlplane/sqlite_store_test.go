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
		RequestID:   "req-test-123",
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
	if audit[0].RequestID != "req-test-123" {
		t.Fatalf("expected persisted request ID")
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

func TestSQLiteStorePersistsToolCalls(t *testing.T) {
	path := filepath.Join(t.TempDir(), "controlplane.sqlite3")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	record := store.AppendToolCall(ToolCallRecord{
		At:          "2026-07-09T00:00:02Z",
		RequestID:   "req-tool-123",
		OrgID:       "default",
		ActorUserID: "local-user",
		AgentRunID:  "run_123",
		ServiceID:   "backend",
		Environment: "prod",
		Capability:  "metrics",
		Action:      "get_service_health",
		Arguments: map[string]any{
			"target": "backend-prod",
		},
		RiskLevel: RiskRead,
		Decision:  DecisionAllowed,
		Provider:  "mock",
		RouteTrace: &ProviderRouteTrace{
			CapabilityID:             "metrics.get_service_health",
			SelectedProvider:         "mock",
			SelectedAdapterAvailable: true,
			AlternativeProviders:     []string{"github"},
			Reason:                   "Capability metrics.get_service_health is registered with provider mock.",
		},
		Status: "success",
		Result: map[string]any{
			"status": "degraded",
		},
		Error: &ToolCallError{
			Provider:  "mock",
			Category:  "transient",
			Operation: "metrics.get_service_health",
			Retryable: true,
			Message:   "kept for persistence check",
		},
	})
	if err := store.Close(); err != nil {
		t.Fatalf("close sqlite store: %v", err)
	}

	reopened, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("reopen sqlite store: %v", err)
	}
	defer reopened.Close()

	records := reopened.ToolCalls()
	if len(records) != 1 {
		t.Fatalf("expected one persisted tool call, got %d", len(records))
	}
	if records[0].ID != record.ID || records[0].RequestID != "req-tool-123" {
		t.Fatalf("expected persisted tool call identity")
	}
	if records[0].Arguments["target"] != "backend-prod" {
		t.Fatalf("expected persisted tool call arguments")
	}
	if records[0].Result["status"] != "degraded" {
		t.Fatalf("expected persisted tool call result")
	}
	if records[0].RouteTrace == nil || records[0].RouteTrace.SelectedProvider != "mock" || len(records[0].RouteTrace.AlternativeProviders) != 1 {
		t.Fatalf("expected persisted route trace, got %#v", records[0].RouteTrace)
	}
	if records[0].Error == nil || !records[0].Error.Retryable {
		t.Fatalf("expected persisted tool call error")
	}

	stored, ok := reopened.ToolCall(record.ID)
	if !ok {
		t.Fatalf("expected direct tool call lookup")
	}
	if stored.ID != record.ID {
		t.Fatalf("expected direct lookup to return matching ID")
	}
	if stored.RouteTrace == nil || stored.RouteTrace.CapabilityID != "metrics.get_service_health" {
		t.Fatalf("expected direct lookup route trace")
	}
}

func TestSQLiteStorePersistsConnectors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "controlplane.sqlite3")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	connector := store.CreateConnector(Connector{
		OrgID:      "default",
		Name:       "GitHub code host",
		Provider:   "github",
		Capability: "code_host",
		Config: map[string]any{
			"base_url_set": false,
		},
		SecretRef: "env:GITHUB_TOKEN",
		Status:    ConnectorStatusConfigured,
		Source:    ConnectorSourceAPI,
		CreatedAt: "2026-07-16T00:00:00Z",
		UpdatedAt: "2026-07-16T00:00:00Z",
	})
	if err := store.Close(); err != nil {
		t.Fatalf("close sqlite store: %v", err)
	}

	reopened, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("reopen sqlite store: %v", err)
	}
	defer reopened.Close()

	connectors := reopened.Connectors()
	if len(connectors) != 1 {
		t.Fatalf("expected one persisted connector, got %d", len(connectors))
	}
	if connectors[0].ID != connector.ID || connectors[0].SecretRef != "env:GITHUB_TOKEN" {
		t.Fatalf("expected persisted connector identity")
	}
	if connectors[0].Config["base_url_set"] != false {
		t.Fatalf("expected persisted connector config")
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
