package controlplane

import (
	"fmt"
	"time"
)

const (
	ApprovalPending = "pending"
	ApprovalGranted = "granted"
	ApprovalDenied  = "denied"
)

type ApprovalRequest struct {
	ID           string         `json:"id"`
	Status       string         `json:"status"`
	OrgID        string         `json:"org_id"`
	ActorUserID  string         `json:"actor_user_id"`
	AgentRunID   string         `json:"agent_run_id"`
	ServiceID    string         `json:"service_id"`
	Environment  string         `json:"environment"`
	Capability   string         `json:"capability"`
	Action       string         `json:"action"`
	Arguments    map[string]any `json:"arguments,omitempty"`
	RiskLevel    string         `json:"risk_level"`
	Reason       string         `json:"reason"`
	RequestedAt  string         `json:"requested_at"`
	DecidedAt    string         `json:"decided_at,omitempty"`
	DecidedBy    string         `json:"decided_by,omitempty"`
	DecisionNote string         `json:"decision_note,omitempty"`
}

type ApprovalDecisionRequest struct {
	ActorUserID string `json:"actor_user_id"`
	Reason      string `json:"reason,omitempty"`
}

type ApprovalDecisionResponse struct {
	Status   string          `json:"status"`
	Approval ApprovalRequest `json:"approval"`
}

func newApprovalRequest(id string, req ToolCallRequest, decision PolicyDecision, now time.Time) ApprovalRequest {
	return ApprovalRequest{
		ID:          id,
		Status:      ApprovalPending,
		OrgID:       req.OrgID,
		ActorUserID: req.ActorUserID,
		AgentRunID:  req.AgentRunID,
		ServiceID:   req.ServiceID,
		Environment: req.Environment,
		Capability:  req.Capability,
		Action:      req.Action,
		Arguments:   req.Arguments,
		RiskLevel:   decision.RiskLevel,
		Reason:      decision.Reason,
		RequestedAt: now.UTC().Format(time.RFC3339Nano),
	}
}

func approvalID(seq int) string {
	return fmt.Sprintf("approval_%06d", seq)
}
