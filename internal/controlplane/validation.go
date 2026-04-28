package controlplane

import (
	"fmt"
	"strings"
)

const DecisionInvalid = "invalid"

type RequestValidator interface {
	Validate(req ToolCallRequest, definition CapabilityDefinition) error
}

type StaticRequestValidator struct{}

func (v StaticRequestValidator) Validate(req ToolCallRequest, definition CapabilityDefinition) error {
	if strings.TrimSpace(req.Capability) == "" {
		return fmt.Errorf("capability is required")
	}
	if strings.TrimSpace(req.Action) == "" {
		return fmt.Errorf("action is required")
	}
	if strings.TrimSpace(req.OrgID) == "" {
		return fmt.Errorf("org_id is required")
	}
	if strings.TrimSpace(req.ActorUserID) == "" {
		return fmt.Errorf("actor_user_id is required")
	}
	if strings.TrimSpace(req.AgentRunID) == "" {
		return fmt.Errorf("agent_run_id is required")
	}

	switch definition.ID {
	case "code_host.create_draft_pr":
		if !hasStringArg(req.Arguments, "title") {
			return fmt.Errorf("code_host.create_draft_pr requires title argument")
		}
	case "code_host.get_recent_changes":
		if definition.Provider == GitHubProvider && !hasAnyArg(req.Arguments, "repository", "owner") {
			return fmt.Errorf("github code_host.get_recent_changes requires repository or owner and repo arguments")
		}
	case "deploy.rollback":
		if !hasStringArg(req.Arguments, "target_revision") {
			return fmt.Errorf("deploy.rollback requires target_revision argument")
		}
	case "ci.get_checks":
		if definition.Provider == GitHubProvider && !hasAnyArg(req.Arguments, "ref", "commit_sha", "sha", "head_sha", "pr_number") {
			return fmt.Errorf("github ci.get_checks requires ref, commit_sha, sha, head_sha, or pr_number argument")
		}
	case "ci.get_logs":
		if definition.Provider == GitHubProvider && !hasAnyArg(req.Arguments, "logs_url", "job_id") {
			return fmt.Errorf("github ci.get_logs requires logs_url or job_id argument")
		}
	}

	return nil
}

func hasAnyArg(args map[string]any, keys ...string) bool {
	for _, key := range keys {
		if _, ok := args[key]; ok {
			return true
		}
	}
	return false
}

func hasStringArg(args map[string]any, key string) bool {
	value, ok := args[key]
	if !ok {
		return false
	}
	text, ok := value.(string)
	return ok && strings.TrimSpace(text) != ""
}
