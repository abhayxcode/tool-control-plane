package controlplane

import "strings"

const toolCallRecordStringLimit = 2000

const redactedToolCallValue = "[redacted]"

type redactionTarget string

const (
	redactionTargetArguments redactionTarget = "arguments"
	redactionTargetResults   redactionTarget = "results"
)

type RedactionPolicy struct {
	SensitiveArgumentKeys []string `json:"sensitive_argument_keys,omitempty"`
	SensitiveResultKeys   []string `json:"sensitive_result_keys,omitempty"`
	MaxStringLength       int      `json:"max_string_length,omitempty"`
}

type RedactionPolicyRegistryOptions struct {
	Default              RedactionPolicy            `json:"default,omitempty"`
	ByCapability         map[string]RedactionPolicy `json:"by_capability,omitempty"`
	ByProvider           map[string]RedactionPolicy `json:"by_provider,omitempty"`
	ByCapabilityProvider map[string]RedactionPolicy `json:"by_capability_provider,omitempty"`
}

type RedactionPolicyRegistry struct {
	configured           bool
	defaultPolicy        RedactionPolicy
	byCapability         map[string]RedactionPolicy
	byProvider           map[string]RedactionPolicy
	byCapabilityProvider map[string]RedactionPolicy
}

func DefaultRedactionPolicyRegistry() RedactionPolicyRegistry {
	return RedactionPolicyRegistry{
		configured:    true,
		defaultPolicy: defaultRedactionPolicy(),
		byCapability: map[string]RedactionPolicy{
			"ci.get_logs": {
				SensitiveResultKeys: []string{"logs", "log_excerpt", "raw_log"},
			},
			"code_host.get_file": {
				SensitiveArgumentKeys: []string{"file_content", "content"},
				SensitiveResultKeys:   []string{"content"},
			},
			"runtime.get_workload_status": {
				SensitiveResultKeys: []string{"logs", "log_excerpt", "raw_log"},
			},
		},
		byProvider:           map[string]RedactionPolicy{},
		byCapabilityProvider: map[string]RedactionPolicy{},
	}
}

func NewRedactionPolicyRegistry(options RedactionPolicyRegistryOptions) RedactionPolicyRegistry {
	registry := DefaultRedactionPolicyRegistry()
	registry.defaultPolicy = mergeRedactionPolicy(registry.defaultPolicy, options.Default)
	registry.byCapability = mergeRedactionPolicyMap(registry.byCapability, options.ByCapability)
	registry.byProvider = mergeRedactionPolicyMap(registry.byProvider, options.ByProvider)
	registry.byCapabilityProvider = mergeRedactionPolicyMap(registry.byCapabilityProvider, options.ByCapabilityProvider)
	return registry
}

func (r RedactionPolicyRegistry) Configured() bool {
	return r.configured
}

func (r RedactionPolicyRegistry) PolicyFor(definition CapabilityDefinition) RedactionPolicy {
	if !r.configured {
		r = DefaultRedactionPolicyRegistry()
	}
	policy := r.defaultPolicy
	if providerPolicy, ok := r.byProvider[normalizeRedactionKey(definition.Provider)]; ok {
		policy = mergeRedactionPolicy(policy, providerPolicy)
	}
	if capabilityPolicy, ok := r.byCapability[normalizeRedactionKey(definition.ID)]; ok {
		policy = mergeRedactionPolicy(policy, capabilityPolicy)
	}
	if providerCapabilityPolicy, ok := r.byCapabilityProvider[redactionPolicyRouteKey(definition.ID, definition.Provider)]; ok {
		policy = mergeRedactionPolicy(policy, providerCapabilityPolicy)
	}
	return normalizeRedactionPolicy(policy)
}

func redactToolCallMap(value map[string]any) map[string]any {
	return redactToolCallMapWithPolicy(value, defaultRedactionPolicy(), redactionTargetArguments)
}

func redactToolCallMapWithPolicy(value map[string]any, policy RedactionPolicy, target redactionTarget) map[string]any {
	if len(value) == 0 {
		return nil
	}
	policy = normalizeRedactionPolicy(policy)
	result := make(map[string]any, len(value))
	for key, item := range value {
		result[key] = redactToolCallValue(key, item, policy, target)
	}
	return result
}

func redactToolCallValue(key string, value any, policy RedactionPolicy, target redactionTarget) any {
	if sensitiveToolCallKey(key) || policySensitiveKey(policy, target, key) {
		if emptyToolCallValue(value) {
			return value
		}
		return redactedToolCallValue
	}
	switch typed := value.(type) {
	case map[string]any:
		return redactToolCallMapWithPolicy(typed, policy, target)
	case []any:
		result := make([]any, 0, len(typed))
		for _, item := range typed {
			result = append(result, redactToolCallValue("", item, policy, target))
		}
		return result
	case []map[string]any:
		result := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			result = append(result, redactToolCallMapWithPolicy(item, policy, target))
		}
		return result
	case string:
		return boundToolCallStringWithPolicy(typed, policy)
	default:
		return value
	}
}

func sensitiveToolCallKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	if normalized == "" {
		return false
	}
	for _, marker := range []string{
		"token",
		"secret",
		"password",
		"private_key",
		"authorization",
		"cookie",
		"credential",
		"bearer",
		"file_content",
		"log_excerpt",
		"raw_log",
		"logs",
		"content",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func emptyToolCallValue(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(typed) == ""
	default:
		return false
	}
}

func boundToolCallString(value string) string {
	return boundToolCallStringWithPolicy(value, defaultRedactionPolicy())
}

func boundToolCallStringWithPolicy(value string, policy RedactionPolicy) string {
	limit := policy.MaxStringLength
	if limit <= 0 {
		limit = toolCallRecordStringLimit
	}
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "...[truncated]"
}

func defaultRedactionPolicy() RedactionPolicy {
	return RedactionPolicy{MaxStringLength: toolCallRecordStringLimit}
}

func mergeRedactionPolicy(base RedactionPolicy, override RedactionPolicy) RedactionPolicy {
	base.SensitiveArgumentKeys = append(base.SensitiveArgumentKeys, override.SensitiveArgumentKeys...)
	base.SensitiveResultKeys = append(base.SensitiveResultKeys, override.SensitiveResultKeys...)
	if override.MaxStringLength > 0 {
		base.MaxStringLength = override.MaxStringLength
	}
	return normalizeRedactionPolicy(base)
}

func mergeRedactionPolicyMap(base map[string]RedactionPolicy, override map[string]RedactionPolicy) map[string]RedactionPolicy {
	result := map[string]RedactionPolicy{}
	for key, policy := range base {
		result[normalizeRedactionKey(key)] = normalizeRedactionPolicy(policy)
	}
	for key, policy := range override {
		normalized := normalizeRedactionKey(key)
		result[normalized] = mergeRedactionPolicy(result[normalized], policy)
	}
	return result
}

func normalizeRedactionPolicy(policy RedactionPolicy) RedactionPolicy {
	policy.SensitiveArgumentKeys = normalizeRedactionKeys(policy.SensitiveArgumentKeys)
	policy.SensitiveResultKeys = normalizeRedactionKeys(policy.SensitiveResultKeys)
	return policy
}

func normalizeRedactionKeys(keys []string) []string {
	seen := map[string]struct{}{}
	result := []string{}
	for _, key := range keys {
		normalized := normalizeRedactionKey(key)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}
	return result
}

func normalizeRedactionKey(key string) string {
	return strings.ToLower(strings.TrimSpace(key))
}

func redactionPolicyRouteKey(capabilityID string, provider string) string {
	return normalizeRedactionKey(capabilityID) + "@" + normalizeRedactionKey(provider)
}

func policySensitiveKey(policy RedactionPolicy, target redactionTarget, key string) bool {
	normalized := normalizeRedactionKey(key)
	if normalized == "" {
		return false
	}
	keys := policy.SensitiveArgumentKeys
	if target == redactionTargetResults {
		keys = policy.SensitiveResultKeys
	}
	for _, item := range keys {
		if item == normalized {
			return true
		}
	}
	return false
}

func firstNonEmptyToolCallValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
