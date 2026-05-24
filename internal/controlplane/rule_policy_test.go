package controlplane

import "testing"

func TestRulePolicyEngineRequiresApprovalForMatchingReadTool(t *testing.T) {
	engine, err := NewRulePolicyEngine(PolicyConfig{
		Rules: []PolicyRule{
			{
				ID:     "approval-for-prod-metrics",
				Effect: PolicyEffectRequireApproval,
				Reason: "Production metrics require review.",
				Match: PolicyRuleMatch{
					Environment: "prod",
					Capability:  "metrics",
					Action:      "get_service_health",
				},
			},
		},
	}, StaticPolicyEngine{})
	if err != nil {
		t.Fatalf("new policy engine: %v", err)
	}

	decision := engine.Evaluate(ToolCallRequest{
		OrgID:       "default",
		ActorUserID: "local-user",
		AgentRunID:  "run_123",
		ServiceID:   "backend",
		Environment: "prod",
		Capability:  "metrics",
		Action:      "get_service_health",
	}, DefaultCapabilityRegistry())

	if decision.Decision != DecisionApprovalRequired {
		t.Fatalf("expected approval required, got %q", decision.Decision)
	}
	if !decision.ApprovalRequired {
		t.Fatalf("expected approval required flag")
	}
	if decision.Reason != "Production metrics require review." {
		t.Fatalf("unexpected decision reason: %q", decision.Reason)
	}
}

func TestRulePolicyEngineDeniesMatchingTool(t *testing.T) {
	engine, err := NewRulePolicyEngine(PolicyConfig{
		Rules: []PolicyRule{
			{
				ID:     "deny-prod-runtime",
				Effect: PolicyEffectDeny,
				Match: PolicyRuleMatch{
					Environment: "prod",
					Capability:  "runtime",
				},
			},
		},
	}, StaticPolicyEngine{})
	if err != nil {
		t.Fatalf("new policy engine: %v", err)
	}

	decision := engine.Evaluate(ToolCallRequest{
		OrgID:       "default",
		ActorUserID: "local-user",
		AgentRunID:  "run_123",
		ServiceID:   "backend",
		Environment: "prod",
		Capability:  "runtime",
		Action:      "get_workload_status",
	}, DefaultCapabilityRegistry())

	if decision.Decision != DecisionDenied {
		t.Fatalf("expected denied, got %q", decision.Decision)
	}
	if decision.RiskLevel != RiskRead {
		t.Fatalf("expected read risk, got %q", decision.RiskLevel)
	}
}

func TestRulePolicyEngineCanAllowHighRiskRegisteredTool(t *testing.T) {
	engine, err := NewRulePolicyEngine(PolicyConfig{
		Rules: []PolicyRule{
			{
				ID:     "allow-staging-rollback",
				Effect: PolicyEffectAllow,
				Match: PolicyRuleMatch{
					Environment: "staging",
					Capability:  "deploy",
					Action:      "rollback",
				},
			},
		},
	}, StaticPolicyEngine{})
	if err != nil {
		t.Fatalf("new policy engine: %v", err)
	}

	decision := engine.Evaluate(ToolCallRequest{
		OrgID:       "default",
		ActorUserID: "local-user",
		AgentRunID:  "run_123",
		ServiceID:   "backend",
		Environment: "staging",
		Capability:  "deploy",
		Action:      "rollback",
	}, DefaultCapabilityRegistry())

	if decision.Decision != DecisionAllowed {
		t.Fatalf("expected allowed, got %q", decision.Decision)
	}
	if decision.RiskLevel != RiskWriteHigh {
		t.Fatalf("expected write high risk, got %q", decision.RiskLevel)
	}
}

func TestRulePolicyEngineCannotAllowUnknownTool(t *testing.T) {
	engine, err := NewRulePolicyEngine(PolicyConfig{
		Rules: []PolicyRule{
			{
				ID:     "allow-default",
				Effect: PolicyEffectAllow,
				Match: PolicyRuleMatch{
					OrgID: "default",
				},
			},
		},
	}, StaticPolicyEngine{})
	if err != nil {
		t.Fatalf("new policy engine: %v", err)
	}

	decision := engine.Evaluate(ToolCallRequest{
		OrgID:       "default",
		ActorUserID: "local-user",
		AgentRunID:  "run_123",
		ServiceID:   "backend",
		Environment: "prod",
		Capability:  "database",
		Action:      "drop",
	}, DefaultCapabilityRegistry())

	if decision.Decision != DecisionDenied {
		t.Fatalf("expected unknown tool to remain denied, got %q", decision.Decision)
	}
	if decision.RiskLevel != RiskUnknown {
		t.Fatalf("expected unknown risk, got %q", decision.RiskLevel)
	}
}

func TestParsePolicyConfigValidatesRules(t *testing.T) {
	config, err := ParsePolicyConfig([]byte(`{
		"version": 1,
		"rules": [
			{
				"effect": "deny",
				"match": {"capability": "runtime"}
			}
		]
	}`))
	if err != nil {
		t.Fatalf("parse policy config: %v", err)
	}
	if len(config.Rules) != 1 || config.Rules[0].ID != "rule_001" {
		t.Fatalf("expected generated rule ID")
	}

	if _, err := ParsePolicyConfig([]byte(`{"version":1,"rules":[{"id":"bad","effect":"block","match":{"capability":"runtime"}}]}`)); err == nil {
		t.Fatalf("expected invalid effect error")
	}
	if _, err := ParsePolicyConfig([]byte(`{"version":2,"rules":[]}`)); err == nil {
		t.Fatalf("expected unsupported version error")
	}
	if _, err := ParsePolicyConfig([]byte(`{"version":1,"rules":[{"id":"bad","effect":"deny","match":{}}]}`)); err == nil {
		t.Fatalf("expected empty match error")
	}
}
