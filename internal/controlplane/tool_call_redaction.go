package controlplane

import "strings"

const toolCallRecordStringLimit = 2000

func redactToolCallMap(value map[string]any) map[string]any {
	if len(value) == 0 {
		return nil
	}
	result := make(map[string]any, len(value))
	for key, item := range value {
		result[key] = redactToolCallValue(key, item)
	}
	return result
}

func redactToolCallValue(key string, value any) any {
	if sensitiveToolCallKey(key) {
		if emptyToolCallValue(value) {
			return value
		}
		return "[redacted]"
	}
	switch typed := value.(type) {
	case map[string]any:
		return redactToolCallMap(typed)
	case []any:
		result := make([]any, 0, len(typed))
		for _, item := range typed {
			result = append(result, redactToolCallValue("", item))
		}
		return result
	case []map[string]any:
		result := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			result = append(result, redactToolCallMap(item))
		}
		return result
	case string:
		return boundToolCallString(typed)
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
	if len(value) <= toolCallRecordStringLimit {
		return value
	}
	return value[:toolCallRecordStringLimit] + "...[truncated]"
}

func firstNonEmptyToolCallValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
