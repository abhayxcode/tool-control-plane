package client

type CapabilityDefinition struct {
	ID               string `json:"id"`
	Capability       string `json:"capability"`
	Action           string `json:"action"`
	RiskLevel        string `json:"risk_level"`
	Provider         string `json:"provider"`
	Description      string `json:"description"`
	ApprovalRequired bool   `json:"approval_required"`
}

type ToolCallRequest struct {
	RequestID   string         `json:"request_id,omitempty"`
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
	Error             *ToolCallError `json:"error,omitempty"`
	ApprovalRequired  bool           `json:"approval_required,omitempty"`
	ApprovalRequestID string         `json:"approval_request_id,omitempty"`
}

type ToolCallError struct {
	Provider   string `json:"provider,omitempty"`
	Category   string `json:"category,omitempty"`
	Operation  string `json:"operation,omitempty"`
	StatusCode int    `json:"status_code,omitempty"`
	Attempts   int    `json:"attempts,omitempty"`
	Retryable  bool   `json:"retryable"`
	Message    string `json:"message,omitempty"`
}

type ToolCallRecord struct {
	ID                string         `json:"id"`
	At                string         `json:"at"`
	RequestID         string         `json:"request_id,omitempty"`
	OrgID             string         `json:"org_id"`
	ActorUserID       string         `json:"actor_user_id"`
	AgentRunID        string         `json:"agent_run_id"`
	ServiceID         string         `json:"service_id"`
	Environment       string         `json:"environment"`
	Capability        string         `json:"capability"`
	Action            string         `json:"action"`
	Arguments         map[string]any `json:"arguments,omitempty"`
	RiskLevel         string         `json:"risk_level"`
	Decision          string         `json:"decision"`
	Provider          string         `json:"provider,omitempty"`
	Status            string         `json:"status"`
	Reason            string         `json:"reason,omitempty"`
	Error             *ToolCallError `json:"error,omitempty"`
	ApprovalRequestID string         `json:"approval_request_id,omitempty"`
	Result            map[string]any `json:"result,omitempty"`
}

type AuditEntry struct {
	At                string `json:"at"`
	RequestID         string `json:"request_id,omitempty"`
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

type ApprovalRequest struct {
	ID           string         `json:"id"`
	Status       string         `json:"status"`
	Executed     bool           `json:"executed"`
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
	ExecutedAt   string         `json:"executed_at,omitempty"`
}

type ApprovalDecisionRequest struct {
	ActorUserID string `json:"actor_user_id"`
	Reason      string `json:"reason,omitempty"`
}

type ApprovalDecisionResponse struct {
	Status   string          `json:"status"`
	Approval ApprovalRequest `json:"approval"`
}

type ApprovalExecuteResponse struct {
	Status   string           `json:"status"`
	Approval ApprovalRequest  `json:"approval"`
	ToolCall ToolCallResponse `json:"tool_call,omitempty"`
	Reason   string           `json:"reason,omitempty"`
}

type AuditExportResponse struct {
	SchemaVersion string            `json:"schema_version"`
	ExportedAt    string            `json:"exported_at"`
	Audit         []AuditEntry      `json:"audit"`
	ToolCalls     []ToolCallRecord  `json:"tool_calls"`
	Approvals     []ApprovalRequest `json:"approvals"`
}
