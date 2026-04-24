package controlplane

import (
	"fmt"
	"sync"
	"time"
)

var readActions = map[string]bool{
	"metrics.get_service_health":   true,
	"errors.get_recent_errors":     true,
	"deploy.get_recent_deploys":    true,
	"code_host.get_recent_changes": true,
	"runtime.get_workload_status":  true,
	"docs.search_runbooks":         true,
}

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
	Status    string         `json:"status"`
	RiskLevel string         `json:"risk_level"`
	Provider  string         `json:"provider,omitempty"`
	Result    map[string]any `json:"result,omitempty"`
	Reason    string         `json:"reason,omitempty"`
}

type AuditEntry struct {
	At          string `json:"at"`
	OrgID       string `json:"org_id"`
	ActorUserID string `json:"actor_user_id"`
	AgentRunID  string `json:"agent_run_id"`
	ServiceID   string `json:"service_id"`
	Environment string `json:"environment"`
	Capability  string `json:"capability"`
	Action      string `json:"action"`
	RiskLevel   string `json:"risk_level"`
	Decision    string `json:"decision"`
}

type Service struct {
	mu      sync.Mutex
	audit   []AuditEntry
	fixture map[string]map[string]any
}

func NewService() *Service {
	return &Service{fixture: defaultFixtures()}
}

func (s *Service) Capabilities() []string {
	return []string{
		"metrics.get_service_health",
		"errors.get_recent_errors",
		"deploy.get_recent_deploys",
		"code_host.get_recent_changes",
		"runtime.get_workload_status",
		"docs.search_runbooks",
	}
}

func (s *Service) CallTool(req ToolCallRequest) ToolCallResponse {
	key := req.Capability + "." + req.Action
	riskLevel := "unknown"
	allowed := false
	if readActions[key] {
		riskLevel = "read"
		allowed = true
	}

	decision := "denied"
	if allowed {
		decision = "allowed"
	}
	s.appendAudit(req, riskLevel, decision)

	if !allowed {
		return ToolCallResponse{
			Status:    "denied",
			RiskLevel: riskLevel,
			Reason:    "Mock policy only permits read actions.",
		}
	}

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
		Provider:  "mock",
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

func (s *Service) appendAudit(req ToolCallRequest, riskLevel string, decision string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.audit = append(s.audit, AuditEntry{
		At:          time.Now().UTC().Format(time.RFC3339Nano),
		OrgID:       req.OrgID,
		ActorUserID: req.ActorUserID,
		AgentRunID:  req.AgentRunID,
		ServiceID:   req.ServiceID,
		Environment: req.Environment,
		Capability:  req.Capability,
		Action:      req.Action,
		RiskLevel:   riskLevel,
		Decision:    decision,
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
	}
}
