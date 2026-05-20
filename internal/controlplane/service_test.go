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
	var foundGetFile bool
	var foundGetPullRequest bool
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
		if detail.ID == "code_host.get_file" {
			foundGetFile = true
			if detail.RiskLevel != RiskRead {
				t.Fatalf("expected get file risk %q, got %q", RiskRead, detail.RiskLevel)
			}
		}
		if detail.ID == "code_host.get_pull_request" {
			foundGetPullRequest = true
			if detail.RiskLevel != RiskRead {
				t.Fatalf("expected get pull request risk %q, got %q", RiskRead, detail.RiskLevel)
			}
		}
	}
	if !foundDraftPR {
		t.Fatalf("expected draft PR capability metadata")
	}
	if !foundGetFile {
		t.Fatalf("expected get file capability metadata")
	}
	if !foundGetPullRequest {
		t.Fatalf("expected get pull request capability metadata")
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
		Capability:  "database",
		Action:      "drop",
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

func TestCallToolRejectsInvalidKnownRequest(t *testing.T) {
	svc := NewService()
	result := svc.CallTool(ToolCallRequest{
		OrgID:       "default",
		ActorUserID: "local-user",
		AgentRunID:  "run_123",
		ServiceID:   "backend",
		Environment: "prod",
		Capability:  "code_host",
		Action:      "create_draft_pr",
	})
	if result.Status != DecisionInvalid {
		t.Fatalf("expected invalid, got %q", result.Status)
	}
	if result.RiskLevel != RiskWriteLow {
		t.Fatalf("expected write_low risk, got %q", result.RiskLevel)
	}
	if result.Reason != "code_host.create_draft_pr requires title argument" {
		t.Fatalf("unexpected validation reason: %q", result.Reason)
	}

	audit := svc.Audit()
	if len(audit) != 1 {
		t.Fatalf("expected one audit entry")
	}
	if audit[0].Decision != DecisionInvalid {
		t.Fatalf("expected invalid audit decision, got %q", audit[0].Decision)
	}
}

func TestCallToolRejectsInvalidApprovalRequestBeforeCreatingApproval(t *testing.T) {
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
	if result.Status != DecisionInvalid {
		t.Fatalf("expected invalid, got %q", result.Status)
	}
	if result.ApprovalRequestID != "" {
		t.Fatalf("expected no approval request for invalid tool call")
	}
	if len(svc.Approvals()) != 0 {
		t.Fatalf("expected no approvals")
	}
}

func TestCallToolRejectsInvalidGitHubCIRequestBeforeAdapter(t *testing.T) {
	registry := DefaultCapabilityRegistry().WithProviderOverrides(GitHubProviderOverrides())
	svc := NewServiceWithOptions(ServiceOptions{
		Registry: registry,
		Adapters: DefaultAdapterRegistryWithGitHub(GitHubAdapterConfig{
			Token: "test-token",
		}),
	})
	result := svc.CallTool(ToolCallRequest{
		OrgID:       "default",
		ActorUserID: "local-user",
		AgentRunID:  "run_123",
		ServiceID:   "backend",
		Environment: "prod",
		Capability:  "ci",
		Action:      "get_checks",
		Arguments: map[string]any{
			"repository": "acme/backend",
		},
	})
	if result.Status != DecisionInvalid {
		t.Fatalf("expected invalid, got %q", result.Status)
	}
	if result.Reason != "github ci.get_checks requires ref, commit_sha, sha, head_sha, or pr_number argument" {
		t.Fatalf("unexpected validation reason: %q", result.Reason)
	}
}

func TestCallToolRejectsInvalidGitHubDraftPRRequestBeforeAdapter(t *testing.T) {
	registry := DefaultCapabilityRegistry().WithProviderOverrides(GitHubProviderOverrides())
	svc := NewServiceWithOptions(ServiceOptions{
		Registry: registry,
		Adapters: DefaultAdapterRegistryWithGitHub(GitHubAdapterConfig{
			Token: "test-token",
		}),
	})
	result := svc.CallTool(ToolCallRequest{
		OrgID:       "default",
		ActorUserID: "local-user",
		AgentRunID:  "run_123",
		ServiceID:   "backend",
		Environment: "prod",
		Capability:  "code_host",
		Action:      "create_draft_pr",
		Arguments: map[string]any{
			"repository": "acme/backend",
			"title":      "Draft: Revert backend database pool config",
		},
	})
	if result.Status != DecisionInvalid {
		t.Fatalf("expected invalid, got %q", result.Status)
	}
	if result.Reason != "github code_host.create_draft_pr requires head, head_branch, or branch argument" {
		t.Fatalf("unexpected validation reason: %q", result.Reason)
	}
}

func TestCallToolRejectsInvalidGitHubDeployRequestBeforeAdapter(t *testing.T) {
	registry := DefaultCapabilityRegistry().WithProviderOverrides(GitHubDeployProviderOverrides())
	svc := NewServiceWithOptions(ServiceOptions{
		Registry: registry,
		Adapters: DefaultAdapterRegistryWithGitHub(GitHubAdapterConfig{
			Token: "test-token",
		}),
	})
	result := svc.CallTool(ToolCallRequest{
		OrgID:       "default",
		ActorUserID: "local-user",
		AgentRunID:  "run_123",
		ServiceID:   "backend",
		Environment: "prod",
		Capability:  "deploy",
		Action:      "get_recent_deploys",
		Arguments: map[string]any{
			"workflow": "deploy-backend.yml",
		},
	})
	if result.Status != DecisionInvalid {
		t.Fatalf("expected invalid, got %q", result.Status)
	}
	if result.Reason != "github deploy.get_recent_deploys requires repository or owner and repo arguments" {
		t.Fatalf("unexpected validation reason: %q", result.Reason)
	}
}

func TestCallToolValidatesDraftPRFilesPayload(t *testing.T) {
	svc := NewService()
	valid := svc.CallTool(ToolCallRequest{
		OrgID:       "default",
		ActorUserID: "local-user",
		AgentRunID:  "run_123",
		ServiceID:   "backend",
		Environment: "prod",
		Capability:  "code_host",
		Action:      "create_draft_pr",
		Arguments: map[string]any{
			"title":          "Draft: Revert backend database pool config",
			"commit_message": "Revert backend database pool config",
			"files": map[string]any{
				"config/database.yaml": "max_open_connections: 50\n",
			},
		},
	})
	if valid.Status != "success" {
		t.Fatalf("expected valid files payload, got %q: %s", valid.Status, valid.Reason)
	}

	invalid := svc.CallTool(ToolCallRequest{
		OrgID:       "default",
		ActorUserID: "local-user",
		AgentRunID:  "run_124",
		ServiceID:   "backend",
		Environment: "prod",
		Capability:  "code_host",
		Action:      "create_draft_pr",
		Arguments: map[string]any{
			"title": "Draft: Revert backend database pool config",
			"files": map[string]any{
				"config/database.yaml": 50,
			},
		},
	})
	if invalid.Status != DecisionInvalid {
		t.Fatalf("expected invalid files payload, got %q", invalid.Status)
	}
	if invalid.Reason != "code_host.create_draft_pr files values must be non-empty strings" {
		t.Fatalf("unexpected validation reason: %q", invalid.Reason)
	}
}

func TestCallToolRequiresApprovalForHighRiskAction(t *testing.T) {
	svc := NewService()
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
	if result.RiskLevel != RiskWriteHigh {
		t.Fatalf("expected write_high risk, got %q", result.RiskLevel)
	}
	if !result.ApprovalRequired {
		t.Fatalf("expected approval required flag")
	}
	if result.ApprovalRequestID == "" {
		t.Fatalf("expected approval request ID")
	}

	approval, ok := svc.Approval(result.ApprovalRequestID)
	if !ok {
		t.Fatalf("expected approval request")
	}
	if approval.Status != ApprovalPending {
		t.Fatalf("expected pending approval, got %q", approval.Status)
	}
	if approval.RiskLevel != RiskWriteHigh {
		t.Fatalf("expected approval risk %q, got %q", RiskWriteHigh, approval.RiskLevel)
	}
	if approval.Arguments["target_revision"] != "sha-abc123" {
		t.Fatalf("expected original tool arguments in approval request")
	}

	audit := svc.Audit()
	if len(audit) != 1 {
		t.Fatalf("expected one audit entry")
	}
	if audit[0].Decision != DecisionApprovalRequired {
		t.Fatalf("expected approval audit decision, got %q", audit[0].Decision)
	}
	if audit[0].ApprovalRequestID != result.ApprovalRequestID {
		t.Fatalf("expected audit to reference approval request")
	}
}

func TestGrantApprovalUpdatesPendingRequest(t *testing.T) {
	svc := NewService()
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

	decision, ok := svc.GrantApproval(result.ApprovalRequestID, ApprovalDecisionRequest{
		ActorUserID: "oncall-lead",
		Reason:      "Rollback approved during incident.",
	})
	if !ok {
		t.Fatalf("expected approval decision")
	}
	if decision.Status != ApprovalGranted {
		t.Fatalf("expected granted approval, got %q", decision.Status)
	}
	if decision.Approval.DecidedBy != "oncall-lead" {
		t.Fatalf("expected deciding actor")
	}
	if decision.Approval.DecisionNote == "" {
		t.Fatalf("expected decision note")
	}

	approvals := svc.Approvals()
	if len(approvals) != 1 {
		t.Fatalf("expected one approval")
	}
	if approvals[0].Status != ApprovalGranted {
		t.Fatalf("expected stored approval to be granted")
	}
}

func TestExecuteApprovalRunsGrantedToolOnce(t *testing.T) {
	svc := NewService()
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
	svc.GrantApproval(result.ApprovalRequestID, ApprovalDecisionRequest{
		ActorUserID: "oncall-lead",
		Reason:      "Rollback approved during incident.",
	})

	executed, ok := svc.ExecuteApproval(result.ApprovalRequestID)
	if !ok {
		t.Fatalf("expected approval execution")
	}
	if executed.Status != DecisionApprovedExecuted {
		t.Fatalf("expected approved execution, got %q", executed.Status)
	}
	if executed.ToolCall.Status != "success" {
		t.Fatalf("expected successful tool call, got %q", executed.ToolCall.Status)
	}
	if executed.ToolCall.Result["rollback_id"] != "rollback-123" {
		t.Fatalf("expected rollback fixture result")
	}
	if !executed.Approval.Executed {
		t.Fatalf("expected approval marked executed")
	}
	if executed.Approval.ExecutedAt == "" {
		t.Fatalf("expected executed timestamp")
	}

	audit := svc.Audit()
	if len(audit) != 2 {
		t.Fatalf("expected approval request and approved execution audit entries, got %d", len(audit))
	}
	if audit[1].Decision != DecisionApprovedExecuted {
		t.Fatalf("expected approved execution audit decision, got %q", audit[1].Decision)
	}
	if audit[1].ApprovalRequestID != result.ApprovalRequestID {
		t.Fatalf("expected execution audit to reference approval request")
	}

	secondExecution, ok := svc.ExecuteApproval(result.ApprovalRequestID)
	if !ok {
		t.Fatalf("expected second execution response")
	}
	if secondExecution.Status != "blocked" {
		t.Fatalf("expected second execution blocked, got %q", secondExecution.Status)
	}
}

func TestExecuteApprovalBlocksPendingAndDeniedApprovals(t *testing.T) {
	svc := NewService()
	pending := svc.CallTool(ToolCallRequest{
		OrgID:       "default",
		ActorUserID: "local-user",
		AgentRunID:  "run_123",
		ServiceID:   "backend",
		Environment: "prod",
		Capability:  "deploy",
		Action:      "rollback",
		Arguments: map[string]any{
			"target_revision": "sha-def456",
		},
	})
	pendingExecution, ok := svc.ExecuteApproval(pending.ApprovalRequestID)
	if !ok {
		t.Fatalf("expected pending execution response")
	}
	if pendingExecution.Status != "blocked" {
		t.Fatalf("expected pending approval execution blocked, got %q", pendingExecution.Status)
	}

	denied := svc.CallTool(ToolCallRequest{
		OrgID:       "default",
		ActorUserID: "local-user",
		AgentRunID:  "run_456",
		ServiceID:   "backend",
		Environment: "prod",
		Capability:  "deploy",
		Action:      "rollback",
		Arguments: map[string]any{
			"target_revision": "sha-abc123",
		},
	})
	svc.DenyApproval(denied.ApprovalRequestID, ApprovalDecisionRequest{
		ActorUserID: "oncall-lead",
		Reason:      "Rollback too risky.",
	})
	deniedExecution, ok := svc.ExecuteApproval(denied.ApprovalRequestID)
	if !ok {
		t.Fatalf("expected denied execution response")
	}
	if deniedExecution.Status != "blocked" {
		t.Fatalf("expected denied approval execution blocked, got %q", deniedExecution.Status)
	}
}

func TestDenyApprovalUpdatesPendingRequest(t *testing.T) {
	svc := NewService()
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

	decision, ok := svc.DenyApproval(result.ApprovalRequestID, ApprovalDecisionRequest{
		ActorUserID: "oncall-lead",
		Reason:      "Rollback too risky.",
	})
	if !ok {
		t.Fatalf("expected approval decision")
	}
	if decision.Status != ApprovalDenied {
		t.Fatalf("expected denied approval, got %q", decision.Status)
	}
}

func TestApprovalDecisionReturnsFalseForUnknownID(t *testing.T) {
	svc := NewService()
	_, ok := svc.GrantApproval("approval_missing", ApprovalDecisionRequest{
		ActorUserID: "oncall-lead",
	})
	if ok {
		t.Fatalf("expected unknown approval to be missing")
	}
	_, ok = svc.ExecuteApproval("approval_missing")
	if ok {
		t.Fatalf("expected unknown approval execution to be missing")
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

func TestCallToolAllowsCodeHostGetFileReadAction(t *testing.T) {
	svc := NewService()
	result := svc.CallTool(ToolCallRequest{
		OrgID:       "default",
		ActorUserID: "local-user",
		AgentRunID:  "run_123",
		ServiceID:   "backend",
		Environment: "prod",
		Capability:  "code_host",
		Action:      "get_file",
		Arguments: map[string]any{
			"repository": "acme/backend",
			"path":       "config/database.yaml",
		},
	})
	if result.Status != "success" {
		t.Fatalf("expected success, got %q (%s)", result.Status, result.Reason)
	}
	if result.RiskLevel != "read" {
		t.Fatalf("expected read risk, got %q", result.RiskLevel)
	}
	if result.Result["content"] != "max_open_connections: 5\n" {
		t.Fatalf("expected file content, got %#v", result.Result["content"])
	}
}

func TestCallToolAllowsCodeHostGetPullRequestReadAction(t *testing.T) {
	svc := NewService()
	result := svc.CallTool(ToolCallRequest{
		OrgID:       "default",
		ActorUserID: "local-user",
		AgentRunID:  "run_123",
		ServiceID:   "backend",
		Environment: "prod",
		Capability:  "code_host",
		Action:      "get_pull_request",
		Arguments: map[string]any{
			"repository": "acme/backend",
			"pr_number":  999,
		},
	})
	if result.Status != "success" {
		t.Fatalf("expected success, got %q (%s)", result.Status, result.Reason)
	}
	if result.RiskLevel != "read" {
		t.Fatalf("expected read risk, got %q", result.RiskLevel)
	}
	if result.Result["merged"] != false {
		t.Fatalf("expected unmerged PR, got %#v", result.Result["merged"])
	}
}
