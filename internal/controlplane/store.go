package controlplane

import (
	"fmt"
	"sync"
)

type Store interface {
	AppendAudit(entry AuditEntry)
	Audit() []AuditEntry
	AppendToolCall(record ToolCallRecord) ToolCallRecord
	ToolCalls() []ToolCallRecord
	ToolCall(id string) (ToolCallRecord, bool)
	CreateConnector(connector Connector) Connector
	Connectors() []Connector
	CreateApproval(approval ApprovalRequest) ApprovalRequest
	Approval(id string) (ApprovalRequest, bool)
	Approvals() []ApprovalRequest
	UpdateApproval(approval ApprovalRequest) bool
}

type MemoryStore struct {
	mu              sync.Mutex
	audit           []AuditEntry
	nextToolCallID  int
	toolCallOrder   []string
	toolCalls       map[string]ToolCallRecord
	nextConnectorID int
	connectorOrder  []string
	connectors      map[string]Connector
	nextApprovalID  int
	approvalOrder   []string
	approvals       map[string]ApprovalRequest
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		nextToolCallID:  1,
		toolCalls:       map[string]ToolCallRecord{},
		nextConnectorID: 1,
		connectors:      map[string]Connector{},
		nextApprovalID:  1,
		approvals:       map[string]ApprovalRequest{},
	}
}

func (s *MemoryStore) AppendAudit(entry AuditEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.audit = append(s.audit, entry)
}

func (s *MemoryStore) Audit() []AuditEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]AuditEntry, len(s.audit))
	copy(result, s.audit)
	return result
}

func (s *MemoryStore) AppendToolCall(record ToolCallRecord) ToolCallRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	record.ID = toolCallID(s.nextToolCallID)
	s.nextToolCallID++
	s.toolCalls[record.ID] = record
	s.toolCallOrder = append(s.toolCallOrder, record.ID)
	return record
}

func (s *MemoryStore) ToolCalls() []ToolCallRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]ToolCallRecord, 0, len(s.toolCallOrder))
	for _, id := range s.toolCallOrder {
		result = append(result, s.toolCalls[id])
	}
	return result
}

func (s *MemoryStore) ToolCall(id string) (ToolCallRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.toolCalls[id]
	return record, ok
}

func (s *MemoryStore) CreateConnector(connector Connector) Connector {
	s.mu.Lock()
	defer s.mu.Unlock()
	connector.ID = connectorID(s.nextConnectorID)
	s.nextConnectorID++
	s.connectors[connector.ID] = connector
	s.connectorOrder = append(s.connectorOrder, connector.ID)
	return connector
}

func (s *MemoryStore) Connectors() []Connector {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]Connector, 0, len(s.connectorOrder))
	for _, id := range s.connectorOrder {
		result = append(result, s.connectors[id])
	}
	return result
}

func (s *MemoryStore) CreateApproval(approval ApprovalRequest) ApprovalRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	approval.ID = approvalID(s.nextApprovalID)
	s.nextApprovalID++
	s.approvals[approval.ID] = approval
	s.approvalOrder = append(s.approvalOrder, approval.ID)
	return approval
}

func (s *MemoryStore) Approval(id string) (ApprovalRequest, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	approval, ok := s.approvals[id]
	return approval, ok
}

func (s *MemoryStore) Approvals() []ApprovalRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]ApprovalRequest, 0, len(s.approvalOrder))
	for _, id := range s.approvalOrder {
		result = append(result, s.approvals[id])
	}
	return result
}

func (s *MemoryStore) UpdateApproval(approval ApprovalRequest) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.approvals[approval.ID]; !ok {
		return false
	}
	s.approvals[approval.ID] = approval
	return true
}

func toolCallID(seq int) string {
	return fmt.Sprintf("tool_call_%06d", seq)
}

func connectorID(seq int) string {
	return fmt.Sprintf("connector_%06d", seq)
}
