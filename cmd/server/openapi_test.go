package main

import (
	"os"
	"strings"
	"testing"
)

func TestOpenAPISpecDocumentsHTTPRoutes(t *testing.T) {
	spec, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatalf("read OpenAPI spec: %v", err)
	}
	text := string(spec)
	for _, expected := range []string{
		"/healthz:",
		"/v1/capabilities:",
		"/v1/tool-calls:",
		"/v1/audit:",
		"/v1/approvals:",
		"/v1/approvals/{id}:",
		"/v1/approvals/{id}/grant:",
		"/v1/approvals/{id}/deny:",
		"/v1/approvals/{id}/execute:",
		"ToolCallRequest:",
		"ToolCallResponse:",
		"ApprovalRequest:",
		"AuditEntry:",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("OpenAPI spec missing %q", expected)
		}
	}
}
