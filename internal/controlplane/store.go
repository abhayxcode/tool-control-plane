package controlplane

import "sync"

type Store interface {
	AppendAudit(entry AuditEntry)
	Audit() []AuditEntry
	CreateApproval(approval ApprovalRequest) ApprovalRequest
	Approval(id string) (ApprovalRequest, bool)
	Approvals() []ApprovalRequest
	UpdateApproval(approval ApprovalRequest) bool
}

type MemoryStore struct {
	mu             sync.Mutex
	audit          []AuditEntry
	nextApprovalID int
	approvalOrder  []string
	approvals      map[string]ApprovalRequest
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		nextApprovalID: 1,
		approvals:      map[string]ApprovalRequest{},
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
