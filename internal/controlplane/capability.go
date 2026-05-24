package controlplane

import "sort"

const (
	RiskUnknown   = "unknown"
	RiskRead      = "read"
	RiskWriteLow  = "write_low"
	RiskWriteHigh = "write_high"
)

type CapabilityDefinition struct {
	ID               string `json:"id"`
	Capability       string `json:"capability"`
	Action           string `json:"action"`
	RiskLevel        string `json:"risk_level"`
	Provider         string `json:"provider"`
	Description      string `json:"description"`
	ApprovalRequired bool   `json:"approval_required"`
}

type CapabilityRegistry struct {
	byID map[string]CapabilityDefinition
}

func NewCapabilityRegistry(definitions []CapabilityDefinition) CapabilityRegistry {
	byID := make(map[string]CapabilityDefinition, len(definitions))
	for _, definition := range definitions {
		byID[definition.ID] = definition
	}
	return CapabilityRegistry{byID: byID}
}

func (r CapabilityRegistry) Lookup(capability string, action string) (CapabilityDefinition, bool) {
	definition, ok := r.byID[capability+"."+action]
	return definition, ok
}

func (r CapabilityRegistry) IDs() []string {
	ids := make([]string, 0, len(r.byID))
	for id := range r.byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (r CapabilityRegistry) Details() []CapabilityDefinition {
	ids := r.IDs()
	details := make([]CapabilityDefinition, 0, len(ids))
	for _, id := range ids {
		details = append(details, r.byID[id])
	}
	return details
}

func (r CapabilityRegistry) WithProviderOverrides(overrides map[string]string) CapabilityRegistry {
	details := r.Details()
	for index, detail := range details {
		if provider, ok := overrides[detail.ID]; ok {
			details[index].Provider = provider
		}
	}
	return NewCapabilityRegistry(details)
}

func DefaultCapabilityRegistry() CapabilityRegistry {
	return NewCapabilityRegistry([]CapabilityDefinition{
		{
			ID:          "ci.get_checks",
			Capability:  "ci",
			Action:      "get_checks",
			RiskLevel:   RiskRead,
			Provider:    "mock",
			Description: "Read CI check status for a code change.",
		},
		{
			ID:          "ci.get_logs",
			Capability:  "ci",
			Action:      "get_logs",
			RiskLevel:   RiskRead,
			Provider:    "mock",
			Description: "Read CI logs for a failed or pending workflow.",
		},
		{
			ID:          "code_host.create_draft_pr",
			Capability:  "code_host",
			Action:      "create_draft_pr",
			RiskLevel:   RiskWriteLow,
			Provider:    "mock",
			Description: "Create a draft pull request for human review.",
		},
		{
			ID:          "code_host.update_pull_request",
			Capability:  "code_host",
			Action:      "update_pull_request",
			RiskLevel:   RiskWriteLow,
			Provider:    "mock",
			Description: "Update an existing pull request branch and optionally add a comment.",
		},
		{
			ID:          "code_host.mark_ready_for_review",
			Capability:  "code_host",
			Action:      "mark_ready_for_review",
			RiskLevel:   RiskWriteLow,
			Provider:    "mock",
			Description: "Mark a draft pull request ready for review after caller-side gates pass.",
		},
		{
			ID:          "code_host.get_recent_changes",
			Capability:  "code_host",
			Action:      "get_recent_changes",
			RiskLevel:   RiskRead,
			Provider:    "mock",
			Description: "Read recent code changes for a service.",
		},
		{
			ID:          "code_host.get_file",
			Capability:  "code_host",
			Action:      "get_file",
			RiskLevel:   RiskRead,
			Provider:    "mock",
			Description: "Read a repository file for patch planning and sandbox input hydration.",
		},
		{
			ID:          "code_host.get_pull_request",
			Capability:  "code_host",
			Action:      "get_pull_request",
			RiskLevel:   RiskRead,
			Provider:    "mock",
			Description: "Read pull request merge state and head/base metadata.",
		},
		{
			ID:          "deploy.get_recent_deploys",
			Capability:  "deploy",
			Action:      "get_recent_deploys",
			RiskLevel:   RiskRead,
			Provider:    "mock",
			Description: "Read recent deployment history for a service.",
		},
		{
			ID:               "deploy.rollback",
			Capability:       "deploy",
			Action:           "rollback",
			RiskLevel:        RiskWriteHigh,
			Provider:         "mock",
			Description:      "Rollback a deployment in an environment.",
			ApprovalRequired: true,
		},
		{
			ID:          "docs.search_runbooks",
			Capability:  "docs",
			Action:      "search_runbooks",
			RiskLevel:   RiskRead,
			Provider:    "mock",
			Description: "Search operational runbooks and internal docs.",
		},
		{
			ID:          "errors.get_recent_errors",
			Capability:  "errors",
			Action:      "get_recent_errors",
			RiskLevel:   RiskRead,
			Provider:    "mock",
			Description: "Read recent error events for a service.",
		},
		{
			ID:          "metrics.get_service_health",
			Capability:  "metrics",
			Action:      "get_service_health",
			RiskLevel:   RiskRead,
			Provider:    "mock",
			Description: "Read service health and latency metrics.",
		},
		{
			ID:          "runtime.get_workload_status",
			Capability:  "runtime",
			Action:      "get_workload_status",
			RiskLevel:   RiskRead,
			Provider:    "mock",
			Description: "Read runtime workload health for a service.",
		},
	})
}
