package controlplane

import (
	"encoding/json"
	"fmt"
)

func ParseRedactionPolicyRegistry(data []byte) (RedactionPolicyRegistry, error) {
	var options RedactionPolicyRegistryOptions
	if err := json.Unmarshal(data, &options); err != nil {
		return RedactionPolicyRegistry{}, fmt.Errorf("parse redaction policy config: %w", err)
	}
	if err := validateRedactionPolicyOptions(options); err != nil {
		return RedactionPolicyRegistry{}, err
	}
	return NewRedactionPolicyRegistry(options), nil
}

func validateRedactionPolicyOptions(options RedactionPolicyRegistryOptions) error {
	if options.Default.MaxStringLength < 0 {
		return fmt.Errorf("redaction default max_string_length must not be negative")
	}
	for key, policy := range options.ByCapability {
		if policy.MaxStringLength < 0 {
			return fmt.Errorf("redaction by_capability %q max_string_length must not be negative", key)
		}
	}
	for key, policy := range options.ByProvider {
		if policy.MaxStringLength < 0 {
			return fmt.Errorf("redaction by_provider %q max_string_length must not be negative", key)
		}
	}
	for key, policy := range options.ByCapabilityProvider {
		if policy.MaxStringLength < 0 {
			return fmt.Errorf("redaction by_capability_provider %q max_string_length must not be negative", key)
		}
	}
	return nil
}
