package controlplane

import "time"

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
	registry  CapabilityRegistry
	policy    PolicyEngine
	validator RequestValidator
	adapters  AdapterRegistry
	store     Store
}

type ServiceOptions struct {
	Registry  CapabilityRegistry
	Policy    PolicyEngine
	Validator RequestValidator
	Adapters  AdapterRegistry
	Store     Store
}

func NewService() *Service {
	return NewServiceWithOptions(ServiceOptions{})
}

func NewServiceWithOptions(options ServiceOptions) *Service {
	registry := options.Registry
	if registry.byID == nil {
		registry = DefaultCapabilityRegistry()
	}
	policy := options.Policy
	if policy == nil {
		policy = StaticPolicyEngine{}
	}
	validator := options.Validator
	if validator == nil {
		validator = StaticRequestValidator{}
	}
	adapters := options.Adapters
	if adapters.byProvider == nil {
		adapters = DefaultAdapterRegistry()
	}
	store := options.Store
	if store == nil {
		store = NewMemoryStore()
	}
	return &Service{
		registry:  registry,
		policy:    policy,
		validator: validator,
		adapters:  adapters,
		store:     store,
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
	if decision.Capability.ID != "" {
		if err := s.validator.Validate(req, decision.Capability); err != nil {
			s.appendAudit(req, decision.RiskLevel, DecisionInvalid, "")
			return ToolCallResponse{
				Status:    DecisionInvalid,
				RiskLevel: decision.RiskLevel,
				Reason:    err.Error(),
			}
		}
	}
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
	definition.RiskLevel = riskLevel
	return s.adapters.Execute(definition, req)
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
	definition.RiskLevel = riskLevel
	response := s.adapters.Execute(definition, req)
	if response.Status != "success" {
		return response
	}
	s.appendAudit(req, riskLevel, DecisionApprovedExecuted, approvalID)
	return response
}

func (s *Service) Audit() []AuditEntry {
	return s.store.Audit()
}

func (s *Service) Approval(id string) (ApprovalRequest, bool) {
	return s.store.Approval(id)
}

func (s *Service) Approvals() []ApprovalRequest {
	return s.store.Approvals()
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
	approval := newApprovalRequest(req, decision, time.Now())
	return s.store.CreateApproval(approval)
}

func (s *Service) decideApproval(id string, status string, req ApprovalDecisionRequest) (ApprovalDecisionResponse, bool) {
	approval, ok := s.store.Approval(id)
	if !ok {
		return ApprovalDecisionResponse{}, false
	}
	if approval.Status == ApprovalPending {
		approval.Status = status
		approval.DecidedAt = time.Now().UTC().Format(time.RFC3339Nano)
		approval.DecidedBy = req.ActorUserID
		approval.DecisionNote = req.Reason
		s.store.UpdateApproval(approval)
	}
	return ApprovalDecisionResponse{
		Status:   approval.Status,
		Approval: approval,
	}, true
}

func (s *Service) approvalForExecution(id string) (ApprovalRequest, ToolCallRequest, bool) {
	approval, ok := s.store.Approval(id)
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
	approval, ok := s.store.Approval(id)
	if !ok {
		return
	}
	approval.Executed = true
	approval.ExecutedAt = time.Now().UTC().Format(time.RFC3339Nano)
	s.store.UpdateApproval(approval)
}

func (s *Service) clearApprovalExecution(id string) {
	approval, ok := s.store.Approval(id)
	if !ok {
		return
	}
	approval.Executed = false
	approval.ExecutedAt = ""
	s.store.UpdateApproval(approval)
}

func (s *Service) appendAudit(req ToolCallRequest, riskLevel string, decision string, approvalRequestID string) {
	s.store.AppendAudit(AuditEntry{
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
