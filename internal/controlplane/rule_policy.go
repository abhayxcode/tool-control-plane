package controlplane

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	PolicyEffectAllow           = "allow"
	PolicyEffectDeny            = "deny"
	PolicyEffectRequireApproval = "require_approval"
)

type PolicyConfig struct {
	Version int          `json:"version"`
	Rules   []PolicyRule `json:"rules"`
}

type PolicyRule struct {
	ID     string          `json:"id"`
	Effect string          `json:"effect"`
	Reason string          `json:"reason,omitempty"`
	Match  PolicyRuleMatch `json:"match"`
}

type PolicyRuleMatch struct {
	OrgID       string `json:"org_id,omitempty"`
	ActorUserID string `json:"actor_user_id,omitempty"`
	ServiceID   string `json:"service_id,omitempty"`
	Environment string `json:"environment,omitempty"`
	Capability  string `json:"capability,omitempty"`
	Action      string `json:"action,omitempty"`
	RiskLevel   string `json:"risk_level,omitempty"`
	Provider    string `json:"provider,omitempty"`
}

type RulePolicyEngine struct {
	fallback PolicyEngine
	rules    []PolicyRule
}

func ParsePolicyConfig(data []byte) (PolicyConfig, error) {
	var config PolicyConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return PolicyConfig{}, fmt.Errorf("decode policy config: %w", err)
	}
	if config.Version == 0 {
		config.Version = 1
	}
	if config.Version != 1 {
		return PolicyConfig{}, fmt.Errorf("unsupported policy config version %d", config.Version)
	}
	for index := range config.Rules {
		config.Rules[index] = normalizePolicyRule(config.Rules[index], index)
		if err := validatePolicyRule(config.Rules[index]); err != nil {
			return PolicyConfig{}, fmt.Errorf("policy rule %d: %w", index+1, err)
		}
	}
	return config, nil
}

func NewRulePolicyEngine(config PolicyConfig, fallback PolicyEngine) (RulePolicyEngine, error) {
	if fallback == nil {
		fallback = StaticPolicyEngine{}
	}
	if config.Version == 0 {
		config.Version = 1
	}
	if config.Version != 1 {
		return RulePolicyEngine{}, fmt.Errorf("unsupported policy config version %d", config.Version)
	}
	rules := make([]PolicyRule, len(config.Rules))
	for index, rule := range config.Rules {
		rules[index] = normalizePolicyRule(rule, index)
		if err := validatePolicyRule(rules[index]); err != nil {
			return RulePolicyEngine{}, fmt.Errorf("policy rule %d: %w", index+1, err)
		}
	}
	return RulePolicyEngine{
		fallback: fallback,
		rules:    rules,
	}, nil
}

func (p RulePolicyEngine) Evaluate(req ToolCallRequest, registry CapabilityRegistry) PolicyDecision {
	base := p.fallback.Evaluate(req, registry)
	definition := base.Capability
	if definition.ID == "" {
		lookedUp, ok := registry.Lookup(req.Capability, req.Action)
		if !ok {
			return base
		}
		definition = lookedUp
	}

	for _, rule := range p.rules {
		if !policyRuleMatches(rule.Match, req, definition) {
			continue
		}
		return policyRuleDecision(rule, definition)
	}
	return base
}

func (p RulePolicyEngine) PolicyRules() []PolicyRule {
	result := make([]PolicyRule, len(p.rules))
	copy(result, p.rules)
	return result
}

func normalizePolicyRule(rule PolicyRule, index int) PolicyRule {
	rule.ID = strings.TrimSpace(rule.ID)
	if rule.ID == "" {
		rule.ID = fmt.Sprintf("rule_%03d", index+1)
	}
	rule.Effect = strings.ToLower(strings.TrimSpace(rule.Effect))
	rule.Reason = strings.TrimSpace(rule.Reason)
	rule.Match = normalizePolicyRuleMatch(rule.Match)
	return rule
}

func normalizePolicyRuleMatch(match PolicyRuleMatch) PolicyRuleMatch {
	return PolicyRuleMatch{
		OrgID:       strings.TrimSpace(match.OrgID),
		ActorUserID: strings.TrimSpace(match.ActorUserID),
		ServiceID:   strings.TrimSpace(match.ServiceID),
		Environment: strings.TrimSpace(match.Environment),
		Capability:  strings.ToLower(strings.TrimSpace(match.Capability)),
		Action:      strings.ToLower(strings.TrimSpace(match.Action)),
		RiskLevel:   strings.ToLower(strings.TrimSpace(match.RiskLevel)),
		Provider:    strings.ToLower(strings.TrimSpace(match.Provider)),
	}
}

func validatePolicyRule(rule PolicyRule) error {
	switch rule.Effect {
	case PolicyEffectAllow, PolicyEffectDeny, PolicyEffectRequireApproval:
	default:
		return fmt.Errorf("effect must be allow, deny, or require_approval")
	}
	if rule.Match.empty() {
		return fmt.Errorf("match must include at least one field")
	}
	return nil
}

func (m PolicyRuleMatch) empty() bool {
	return m.OrgID == "" &&
		m.ActorUserID == "" &&
		m.ServiceID == "" &&
		m.Environment == "" &&
		m.Capability == "" &&
		m.Action == "" &&
		m.RiskLevel == "" &&
		m.Provider == ""
}

func policyRuleMatches(match PolicyRuleMatch, req ToolCallRequest, definition CapabilityDefinition) bool {
	return policyValueMatches(match.OrgID, req.OrgID) &&
		policyValueMatches(match.ActorUserID, req.ActorUserID) &&
		policyValueMatches(match.ServiceID, req.ServiceID) &&
		policyValueMatches(match.Environment, req.Environment) &&
		policyValueMatches(match.Capability, definition.Capability) &&
		policyValueMatches(match.Action, definition.Action) &&
		policyValueMatches(match.RiskLevel, definition.RiskLevel) &&
		policyValueMatches(match.Provider, definition.Provider)
}

func policyValueMatches(expected string, actual string) bool {
	expected = strings.TrimSpace(expected)
	if expected == "" || expected == "*" {
		return true
	}
	return strings.EqualFold(expected, strings.TrimSpace(actual))
}

func policyRuleDecision(rule PolicyRule, definition CapabilityDefinition) PolicyDecision {
	reason := rule.Reason
	switch rule.Effect {
	case PolicyEffectAllow:
		return PolicyDecision{
			Decision:   DecisionAllowed,
			RiskLevel:  definition.RiskLevel,
			Reason:     reason,
			Capability: definition,
		}
	case PolicyEffectDeny:
		if reason == "" {
			reason = "Tool action denied by policy rule " + rule.ID + "."
		}
		return PolicyDecision{
			Decision:   DecisionDenied,
			RiskLevel:  definition.RiskLevel,
			Reason:     reason,
			Capability: definition,
		}
	case PolicyEffectRequireApproval:
		if reason == "" {
			reason = "Tool action requires approval by policy rule " + rule.ID + "."
		}
		return PolicyDecision{
			Decision:         DecisionApprovalRequired,
			RiskLevel:        definition.RiskLevel,
			Reason:           reason,
			Capability:       definition,
			ApprovalRequired: true,
		}
	default:
		return PolicyDecision{
			Decision:   DecisionDenied,
			RiskLevel:  definition.RiskLevel,
			Reason:     "Policy rule has invalid effect.",
			Capability: definition,
		}
	}
}
