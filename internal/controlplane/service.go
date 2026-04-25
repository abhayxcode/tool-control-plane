package controlplane

import (
	"fmt"
	"sync"
	"time"
)

type ToolCallRequest struct {
	OrgID       string         `json:"org_id"`
	ActorUserID string         `json:"actor_user_id"`
	AgentRunID  string         `json:"agent_run_id"`
	ServiceID   string         `json:"service_id"`
	Environment string         `json:"environment"`
	Capability  string         `json:"capability"`
	Action      string         `json:"action"`
	Arguments   map[string]any `json:"arguments,omitempty"`
}

type ToolCallResponse struct {
	Status            string         `json:"status"`
	RiskLevel         string         `json:"risk_level"`
	Provider          string         `json:"provider,omitempty"`
	Result            map[string]any `json:"result,omitempty"`
	Reason            string         `json:"reason,omitempty"`
	ApprovalRequired  bool           `json:"approval_required,omitempty"`
	ApprovalRequestID string         `json:"approval_request_id,omitempty"`
}

type AuditEntry struct {
	At                string `json:"at"`
	OrgID             string `json:"org_id"`
	ActorUserID       string `json:"actor_user_id"`
	AgentRunID        string `json:"agent_run_id"`
	ServiceID         string `json:"service_id"`
	Environment       string `json:"environment"`
	Capability        string `json:"capability"`
	Action            string `json:"action"`
	RiskLevel         string `json:"risk_level"`
	Decision          string `json:"decision"`
	ApprovalRequestID string `json:"approval_request_id,omitempty"`
}

type Service struct {
	mu             sync.Mutex
	audit          []AuditEntry
	registry       CapabilityRegistry
	policy         PolicyEngine
	fixture        map[string]map[string]any
	nextApprovalID int
	approvalOrder  []string
	approvals      map[string]ApprovalRequest
}

func NewService() *Service {
	return &Service{
		registry:       DefaultCapabilityRegistry(),
		policy:         StaticPolicyEngine{},
		fixture:        defaultFixtures(),
		nextApprovalID: 1,
		approvals:      map[string]ApprovalRequest{},
	}
}

func (s *Service) Capabilities() []string {
	return s.registry.IDs()
}

func (s *Service) CapabilityDetails() []CapabilityDefinition {
	return s.registry.Details()
}

func (s *Service) CallTool(req ToolCallRequest) ToolCallResponse {
	decision := s.policy.Evaluate(req, s.registry)
	if decision.Decision != DecisionAllowed {
		approvalRequestID := ""
		if decision.Decision == DecisionApprovalRequired {
			approval := s.createApprovalRequest(req, decision)
			approvalRequestID = approval.ID
		}
		s.appendAudit(req, decision.RiskLevel, decision.Decision, approvalRequestID)
		return ToolCallResponse{
			Status:            decision.Decision,
			RiskLevel:         decision.RiskLevel,
			Reason:            decision.Reason,
			ApprovalRequired:  decision.ApprovalRequired,
			ApprovalRequestID: approvalRequestID,
		}
	}

	s.appendAudit(req, decision.RiskLevel, decision.Decision, "")
	return s.executeAllowedTool(req, decision.Capability, decision.RiskLevel)
}

func (s *Service) executeAllowedTool(req ToolCallRequest, definition CapabilityDefinition, riskLevel string) ToolCallResponse {
	key := definition.ID
	result, ok := s.fixture[key]
	if !ok {
		return ToolCallResponse{
			Status:    "error",
			RiskLevel: riskLevel,
			Reason:    fmt.Sprintf("No fixture for tool '%s'.", key),
		}
	}

	return ToolCallResponse{
		Status:    "success",
		RiskLevel: riskLevel,
		Provider:  definition.Provider,
		Result:    result,
	}
}

func (s *Service) executeApprovedTool(req ToolCallRequest, approvalID string) ToolCallResponse {
	definition, ok := s.registry.Lookup(req.Capability, req.Action)
	riskLevel := RiskUnknown
	if ok {
		riskLevel = definition.RiskLevel
	}
	if !ok {
		return ToolCallResponse{
			Status:    "error",
			RiskLevel: riskLevel,
			Reason:    "Approved tool action is no longer registered.",
		}
	}
	key := definition.ID
	result, ok := s.fixture[key]
	if !ok {
		return ToolCallResponse{
			Status:    "error",
			RiskLevel: riskLevel,
			Reason:    fmt.Sprintf("No fixture for tool '%s'.", key),
		}
	}
	s.appendAudit(req, riskLevel, DecisionApprovedExecuted, approvalID)

	return ToolCallResponse{
		Status:    "success",
		RiskLevel: riskLevel,
		Provider:  definition.Provider,
		Result:    result,
	}
}

func (s *Service) Audit() []AuditEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]AuditEntry, len(s.audit))
	copy(result, s.audit)
	return result
}

func (s *Service) Approval(id string) (ApprovalRequest, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	approval, ok := s.approvals[id]
	return approval, ok
}

func (s *Service) Approvals() []ApprovalRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]ApprovalRequest, 0, len(s.approvalOrder))
	for _, id := range s.approvalOrder {
		result = append(result, s.approvals[id])
	}
	return result
}

func (s *Service) GrantApproval(id string, req ApprovalDecisionRequest) (ApprovalDecisionResponse, bool) {
	return s.decideApproval(id, ApprovalGranted, req)
}

func (s *Service) DenyApproval(id string, req ApprovalDecisionRequest) (ApprovalDecisionResponse, bool) {
	return s.decideApproval(id, ApprovalDenied, req)
}

func (s *Service) ExecuteApproval(id string) (ApprovalExecuteResponse, bool) {
	approval, toolReq, ok := s.approvalForExecution(id)
	if !ok {
		return ApprovalExecuteResponse{}, false
	}
	if approval.Status != ApprovalGranted {
		return ApprovalExecuteResponse{
			Status:   "blocked",
			Approval: approval,
			Reason:   "Approval must be granted before execution.",
		}, true
	}
	if approval.Executed {
		return ApprovalExecuteResponse{
			Status:   "blocked",
			Approval: approval,
			Reason:   "Approval has already been executed.",
		}, true
	}

	s.markApprovalExecuted(id)
	toolCall := s.executeApprovedTool(toolReq, id)
	if toolCall.Status != "success" {
		s.clearApprovalExecution(id)
		approval, _ = s.Approval(id)
		return ApprovalExecuteResponse{
			Status:   "error",
			Approval: approval,
			ToolCall: toolCall,
			Reason:   toolCall.Reason,
		}, true
	}

	approval, _ = s.Approval(id)
	return ApprovalExecuteResponse{
		Status:   DecisionApprovedExecuted,
		Approval: approval,
		ToolCall: toolCall,
	}, true
}

func (s *Service) createApprovalRequest(req ToolCallRequest, decision PolicyDecision) ApprovalRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := approvalID(s.nextApprovalID)
	s.nextApprovalID++
	approval := newApprovalRequest(id, req, decision, time.Now())
	s.approvals[id] = approval
	s.approvalOrder = append(s.approvalOrder, id)
	return approval
}

func (s *Service) decideApproval(id string, status string, req ApprovalDecisionRequest) (ApprovalDecisionResponse, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	approval, ok := s.approvals[id]
	if !ok {
		return ApprovalDecisionResponse{}, false
	}
	if approval.Status == ApprovalPending {
		approval.Status = status
		approval.DecidedAt = time.Now().UTC().Format(time.RFC3339Nano)
		approval.DecidedBy = req.ActorUserID
		approval.DecisionNote = req.Reason
		s.approvals[id] = approval
	}
	return ApprovalDecisionResponse{
		Status:   approval.Status,
		Approval: approval,
	}, true
}

func (s *Service) approvalForExecution(id string) (ApprovalRequest, ToolCallRequest, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	approval, ok := s.approvals[id]
	if !ok {
		return ApprovalRequest{}, ToolCallRequest{}, false
	}
	toolReq := ToolCallRequest{
		OrgID:       approval.OrgID,
		ActorUserID: approval.ActorUserID,
		AgentRunID:  approval.AgentRunID,
		ServiceID:   approval.ServiceID,
		Environment: approval.Environment,
		Capability:  approval.Capability,
		Action:      approval.Action,
		Arguments:   approval.Arguments,
	}
	return approval, toolReq, true
}

func (s *Service) markApprovalExecuted(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	approval, ok := s.approvals[id]
	if !ok {
		return
	}
	approval.Executed = true
	approval.ExecutedAt = time.Now().UTC().Format(time.RFC3339Nano)
	s.approvals[id] = approval
}

func (s *Service) clearApprovalExecution(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	approval, ok := s.approvals[id]
	if !ok {
		return
	}
	approval.Executed = false
	approval.ExecutedAt = ""
	s.approvals[id] = approval
}

func (s *Service) appendAudit(req ToolCallRequest, riskLevel string, decision string, approvalRequestID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.audit = append(s.audit, AuditEntry{
		At:                time.Now().UTC().Format(time.RFC3339Nano),
		OrgID:             req.OrgID,
		ActorUserID:       req.ActorUserID,
		AgentRunID:        req.AgentRunID,
		ServiceID:         req.ServiceID,
		Environment:       req.Environment,
		Capability:        req.Capability,
		Action:            req.Action,
		RiskLevel:         riskLevel,
		Decision:          decision,
		ApprovalRequestID: approvalRequestID,
	})
}

func defaultFixtures() map[string]map[string]any {
	return map[string]map[string]any{
		"metrics.get_service_health": {
			"status":                      "degraded",
			"latency_p95_ms":              820,
			"error_rate_percent":          7.8,
			"baseline_error_rate_percent": 0.4,
			"evidence":                    "5xx rate increased from 0.4% to 7.8% in the last 30 minutes.",
			"source_url":                  "https://metrics.example.local/backend-prod",
		},
		"errors.get_recent_errors": {
			"top_errors": []map[string]any{
				{
					"message":    "database connection timeout",
					"count":      431,
					"first_seen": "2026-07-09T09:42:00Z",
				},
			},
			"evidence":   "Sentry shows 431 database connection timeout errors after the latest deploy.",
			"source_url": "https://sentry.example.local/projects/backend-api/issues/123",
		},
		"deploy.get_recent_deploys": {
			"deploys": []map[string]any{
				{
					"version":    "sha-abc123",
					"started_at": "2026-07-09T09:38:00Z",
					"status":     "succeeded",
					"actor":      "github-actions",
				},
			},
			"evidence":   "Deployment sha-abc123 completed four minutes before the error spike.",
			"source_url": "https://github.com/acme/backend/actions/runs/1001",
		},
		"deploy.rollback": {
			"rollback_id":     "rollback-123",
			"target_revision": "sha-abc123",
			"status":          "started",
			"evidence":        "Mock rollback to sha-abc123 started after approval.",
			"source_url":      "https://deploy.example.local/backend-prod/rollbacks/rollback-123",
		},
		"code_host.get_recent_changes": {
			"changes": []map[string]any{
				{
					"pr":      456,
					"title":   "Tune database pool defaults",
					"files":   []string{"config/database.yaml"},
					"summary": "Changed max_open_connections from 50 to 5.",
				},
			},
			"evidence":   "Recent PR #456 reduced database pool size from 50 to 5.",
			"source_url": "https://github.com/acme/backend/pull/456",
		},
		"runtime.get_workload_status": {
			"pods_ready":             "5/8",
			"restart_count_last_30m": 12,
			"evidence":               "Three backend pods are not ready and restart count increased after deploy.",
			"source_url":             "https://k8s.example.local/namespaces/prod/deployments/backend-api",
		},
		"docs.search_runbooks": {
			"matches": []map[string]any{
				{
					"title":   "Backend database pool exhaustion",
					"summary": "If DB timeouts rise after deploy, compare pool config and rollback config-only changes first.",
				},
			},
			"evidence":   "Runbook recommends checking pool config and rolling back config-only changes for DB timeout spikes.",
			"source_url": "https://docs.example.local/backend-oncall",
		},
		"code_host.create_draft_pr": {
			"pr_number": 999,
			"branch":    "majdoor/revert-db-pool-config",
			"title":     "Draft: Revert backend database pool config",
			"url":       "https://github.com/acme/backend/pull/999",
			"evidence":  "Draft PR #999 created from validated patch artifact.",
		},
		"ci.get_checks": {
			"status":     "passed",
			"workflow":   "backend-ci.yml",
			"commit_sha": "mock-sha-999",
			"checks": []map[string]any{
				{
					"name":       "unit-tests",
					"conclusion": "success",
				},
				{
					"name":       "config-validation",
					"conclusion": "success",
				},
			},
			"evidence":   "GitHub Actions mock CI passed for draft PR #999.",
			"source_url": "https://github.com/acme/backend/actions/runs/999",
		},
		"ci.get_logs": {
			"summary":    "No failing CI logs. All mock checks passed.",
			"evidence":   "CI logs contain no failures.",
			"source_url": "https://github.com/acme/backend/actions/runs/999/logs",
		},
	}
}
