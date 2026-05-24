package controlplane

import "testing"

func TestParseRedactionPolicyRegistry(t *testing.T) {
	registry, err := ParseRedactionPolicyRegistry([]byte(`{
		"default": {
			"max_string_length": 24,
			"sensitive_argument_keys": ["global_arg"]
		},
		"by_provider": {
			"custom": {
				"sensitive_result_keys": ["provider_result"]
			}
		},
		"by_capability": {
			"metrics.get_service_health": {
				"sensitive_argument_keys": ["capability_arg"]
			}
		},
		"by_capability_provider": {
			"metrics.get_service_health@custom": {
				"sensitive_result_keys": ["route_result"],
				"max_string_length": 12
			}
		}
	}`))
	if err != nil {
		t.Fatalf("parse redaction policy registry: %v", err)
	}

	policy := registry.PolicyFor(CapabilityDefinition{
		ID:       "metrics.get_service_health",
		Provider: "custom",
	})
	if policy.MaxStringLength != 12 {
		t.Fatalf("expected route max string length, got %d", policy.MaxStringLength)
	}
	if !policySensitiveKey(policy, redactionTargetArguments, "global_arg") {
		t.Fatalf("expected default argument key")
	}
	if !policySensitiveKey(policy, redactionTargetArguments, "capability_arg") {
		t.Fatalf("expected capability argument key")
	}
	if !policySensitiveKey(policy, redactionTargetResults, "provider_result") {
		t.Fatalf("expected provider result key")
	}
	if !policySensitiveKey(policy, redactionTargetResults, "route_result") {
		t.Fatalf("expected route result key")
	}
}

func TestParseRedactionPolicyRegistryRejectsInvalidConfig(t *testing.T) {
	_, err := ParseRedactionPolicyRegistry([]byte(`{
		"by_provider": {
			"custom": {
				"max_string_length": -1
			}
		}
	}`))
	if err == nil {
		t.Fatalf("expected invalid max string length error")
	}
}
