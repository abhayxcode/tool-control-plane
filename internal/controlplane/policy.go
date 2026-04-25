package controlplane

const (
	DecisionAllowed          = "allowed"
	DecisionDenied           = "denied"
	DecisionApprovalRequired = "approval_required"
)

type PolicyDecision struct {
	Decision         string
	RiskLevel        string
	Reason           string
	Capability       CapabilityDefinition
	ApprovalRequired bool
}

type PolicyEngine interface {
	Evaluate(req ToolCallRequest, registry CapabilityRegistry) PolicyDecision
}

type StaticPolicyEngine struct{}

func (p StaticPolicyEngine) Evaluate(req ToolCallRequest, registry CapabilityRegistry) PolicyDecision {
	definition, ok := registry.Lookup(req.Capability, req.Action)
	if !ok {
		return PolicyDecision{
			Decision:  DecisionDenied,
			RiskLevel: RiskUnknown,
			Reason:    "No registered capability allows this tool action.",
		}
	}

	if definition.ApprovalRequired || definition.RiskLevel == RiskWriteHigh {
		return PolicyDecision{
			Decision:         DecisionApprovalRequired,
			RiskLevel:        definition.RiskLevel,
			Reason:           "Tool action requires approval before execution.",
			Capability:       definition,
			ApprovalRequired: true,
		}
	}

	return PolicyDecision{
		Decision:   DecisionAllowed,
		RiskLevel:  definition.RiskLevel,
		Capability: definition,
	}
}
