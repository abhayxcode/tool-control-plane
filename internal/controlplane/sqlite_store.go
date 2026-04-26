package controlplane

import (
	"database/sql"
	"encoding/json"
	"fmt"

	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db *sql.DB
}

func NewSQLiteStore(path string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &SQLiteStore{db: db}
	if err := store.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func (s *SQLiteStore) AppendAudit(entry AuditEntry) {
	_, err := s.db.Exec(`
		INSERT INTO audit_entries (
			at, request_id, org_id, actor_user_id, agent_run_id, service_id, environment,
			capability, action, risk_level, decision, approval_request_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.At,
		entry.RequestID,
		entry.OrgID,
		entry.ActorUserID,
		entry.AgentRunID,
		entry.ServiceID,
		entry.Environment,
		entry.Capability,
		entry.Action,
		entry.RiskLevel,
		entry.Decision,
		entry.ApprovalRequestID,
	)
	panicOnStoreError(err)
}

func (s *SQLiteStore) Audit() []AuditEntry {
	rows, err := s.db.Query(`
		SELECT at, request_id, org_id, actor_user_id, agent_run_id, service_id, environment,
			capability, action, risk_level, decision, approval_request_id
		FROM audit_entries
		ORDER BY seq ASC`)
	panicOnStoreError(err)
	defer rows.Close()

	var result []AuditEntry
	for rows.Next() {
		var entry AuditEntry
		err := rows.Scan(
			&entry.At,
			&entry.RequestID,
			&entry.OrgID,
			&entry.ActorUserID,
			&entry.AgentRunID,
			&entry.ServiceID,
			&entry.Environment,
			&entry.Capability,
			&entry.Action,
			&entry.RiskLevel,
			&entry.Decision,
			&entry.ApprovalRequestID,
		)
		panicOnStoreError(err)
		result = append(result, entry)
	}
	panicOnStoreError(rows.Err())
	return result
}

func (s *SQLiteStore) CreateApproval(approval ApprovalRequest) ApprovalRequest {
	tx, err := s.db.Begin()
	panicOnStoreError(err)
	defer tx.Rollback()

	result, err := tx.Exec(`INSERT INTO approval_ids DEFAULT VALUES`)
	panicOnStoreError(err)
	seq, err := result.LastInsertId()
	panicOnStoreError(err)
	approval.ID = approvalID(int(seq))
	argsJSON := mustMarshalArgs(approval.Arguments)

	_, err = tx.Exec(`
		INSERT INTO approvals (
			id, seq, status, executed, org_id, actor_user_id, agent_run_id,
			service_id, environment, capability, action, arguments_json,
			risk_level, reason, requested_at, decided_at, decided_by,
			decision_note, executed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		approval.ID,
		seq,
		approval.Status,
		boolToInt(approval.Executed),
		approval.OrgID,
		approval.ActorUserID,
		approval.AgentRunID,
		approval.ServiceID,
		approval.Environment,
		approval.Capability,
		approval.Action,
		argsJSON,
		approval.RiskLevel,
		approval.Reason,
		approval.RequestedAt,
		approval.DecidedAt,
		approval.DecidedBy,
		approval.DecisionNote,
		approval.ExecutedAt,
	)
	panicOnStoreError(err)
	panicOnStoreError(tx.Commit())
	return approval
}

func (s *SQLiteStore) Approval(id string) (ApprovalRequest, bool) {
	row := s.db.QueryRow(`
		SELECT id, status, executed, org_id, actor_user_id, agent_run_id,
			service_id, environment, capability, action, arguments_json,
			risk_level, reason, requested_at, decided_at, decided_by,
			decision_note, executed_at
		FROM approvals
		WHERE id = ?`, id)
	approval, ok := scanApproval(row)
	return approval, ok
}

func (s *SQLiteStore) Approvals() []ApprovalRequest {
	rows, err := s.db.Query(`
		SELECT id, status, executed, org_id, actor_user_id, agent_run_id,
			service_id, environment, capability, action, arguments_json,
			risk_level, reason, requested_at, decided_at, decided_by,
			decision_note, executed_at
		FROM approvals
		ORDER BY seq ASC`)
	panicOnStoreError(err)
	defer rows.Close()

	var result []ApprovalRequest
	for rows.Next() {
		approval, ok := scanApproval(rows)
		if ok {
			result = append(result, approval)
		}
	}
	panicOnStoreError(rows.Err())
	return result
}

func (s *SQLiteStore) UpdateApproval(approval ApprovalRequest) bool {
	argsJSON := mustMarshalArgs(approval.Arguments)
	result, err := s.db.Exec(`
		UPDATE approvals
		SET status = ?, executed = ?, org_id = ?, actor_user_id = ?,
			agent_run_id = ?, service_id = ?, environment = ?, capability = ?,
			action = ?, arguments_json = ?, risk_level = ?, reason = ?,
			requested_at = ?, decided_at = ?, decided_by = ?,
			decision_note = ?, executed_at = ?
		WHERE id = ?`,
		approval.Status,
		boolToInt(approval.Executed),
		approval.OrgID,
		approval.ActorUserID,
		approval.AgentRunID,
		approval.ServiceID,
		approval.Environment,
		approval.Capability,
		approval.Action,
		argsJSON,
		approval.RiskLevel,
		approval.Reason,
		approval.RequestedAt,
		approval.DecidedAt,
		approval.DecidedBy,
		approval.DecisionNote,
		approval.ExecutedAt,
		approval.ID,
	)
	panicOnStoreError(err)
	affected, err := result.RowsAffected()
	panicOnStoreError(err)
	return affected > 0
}

func (s *SQLiteStore) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS audit_entries (
			seq INTEGER PRIMARY KEY AUTOINCREMENT,
			at TEXT NOT NULL,
			request_id TEXT NOT NULL DEFAULT '',
			org_id TEXT NOT NULL,
			actor_user_id TEXT NOT NULL,
			agent_run_id TEXT NOT NULL,
			service_id TEXT NOT NULL,
			environment TEXT NOT NULL,
			capability TEXT NOT NULL,
			action TEXT NOT NULL,
			risk_level TEXT NOT NULL,
			decision TEXT NOT NULL,
			approval_request_id TEXT NOT NULL DEFAULT ''
		);

		CREATE TABLE IF NOT EXISTS approval_ids (
			seq INTEGER PRIMARY KEY AUTOINCREMENT
		);

		CREATE TABLE IF NOT EXISTS approvals (
			id TEXT PRIMARY KEY,
			seq INTEGER NOT NULL UNIQUE,
			status TEXT NOT NULL,
			executed INTEGER NOT NULL DEFAULT 0,
			org_id TEXT NOT NULL,
			actor_user_id TEXT NOT NULL,
			agent_run_id TEXT NOT NULL,
			service_id TEXT NOT NULL,
			environment TEXT NOT NULL,
			capability TEXT NOT NULL,
			action TEXT NOT NULL,
			arguments_json TEXT NOT NULL DEFAULT '{}',
			risk_level TEXT NOT NULL,
			reason TEXT NOT NULL,
			requested_at TEXT NOT NULL,
			decided_at TEXT NOT NULL DEFAULT '',
			decided_by TEXT NOT NULL DEFAULT '',
			decision_note TEXT NOT NULL DEFAULT '',
			executed_at TEXT NOT NULL DEFAULT ''
		);
	`)
	return err
}

type approvalScanner interface {
	Scan(dest ...any) error
}

func scanApproval(scanner approvalScanner) (ApprovalRequest, bool) {
	var approval ApprovalRequest
	var executed int
	var argsJSON string
	err := scanner.Scan(
		&approval.ID,
		&approval.Status,
		&executed,
		&approval.OrgID,
		&approval.ActorUserID,
		&approval.AgentRunID,
		&approval.ServiceID,
		&approval.Environment,
		&approval.Capability,
		&approval.Action,
		&argsJSON,
		&approval.RiskLevel,
		&approval.Reason,
		&approval.RequestedAt,
		&approval.DecidedAt,
		&approval.DecidedBy,
		&approval.DecisionNote,
		&approval.ExecutedAt,
	)
	if err == sql.ErrNoRows {
		return ApprovalRequest{}, false
	}
	panicOnStoreError(err)
	approval.Executed = executed == 1
	approval.Arguments = mustUnmarshalArgs(argsJSON)
	return approval, true
}

func mustMarshalArgs(args map[string]any) string {
	if args == nil {
		args = map[string]any{}
	}
	result, err := json.Marshal(args)
	panicOnStoreError(err)
	return string(result)
}

func mustUnmarshalArgs(value string) map[string]any {
	if value == "" {
		return map[string]any{}
	}
	var result map[string]any
	err := json.Unmarshal([]byte(value), &result)
	panicOnStoreError(err)
	if result == nil {
		return map[string]any{}
	}
	return result
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func panicOnStoreError(err error) {
	if err != nil {
		panic(fmt.Sprintf("controlplane store error: %v", err))
	}
}
