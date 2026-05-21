package controlplane

import "fmt"

type MockAdapter struct {
	fixtures map[string]map[string]any
}

func NewMockAdapter(fixtures map[string]map[string]any) MockAdapter {
	copied := make(map[string]map[string]any, len(fixtures))
	for key, value := range fixtures {
		copied[key] = value
	}
	return MockAdapter{fixtures: copied}
}

func (a MockAdapter) Execute(definition CapabilityDefinition, req ToolCallRequest) (map[string]any, error) {
	result, ok := a.fixtures[definition.ID]
	if !ok {
		return nil, fmt.Errorf("no fixture for tool '%s'", definition.ID)
	}
	return result, nil
}

func defaultMockFixtures() map[string]map[string]any {
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
		"code_host.get_file": {
			"path":       "config/database.yaml",
			"content":    "max_open_connections: 5\n",
			"source_url": "https://github.com/acme/backend/blob/main/config/database.yaml",
			"evidence":   "Read config/database.yaml from mock repository fixture.",
		},
		"code_host.get_pull_request": {
			"pr_number": 999,
			"state":     "open",
			"merged":    false,
			"branch":    "majdoor/revert-db-pool-config",
			"base":      "main",
			"head_sha":  "mock-sha-999",
			"url":       "https://github.com/acme/backend/pull/999",
			"evidence":  "Mock draft PR #999 is open and not merged.",
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
		"code_host.update_pull_request": {
			"pr_number":   999,
			"branch":      "majdoor/revert-db-pool-config",
			"title":       "Draft: Revert backend database pool config",
			"url":         "https://github.com/acme/backend/pull/999",
			"comment_url": "https://github.com/acme/backend/pull/999#issuecomment-1",
			"evidence":    "Draft PR #999 updated from follow-up patch artifact.",
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
