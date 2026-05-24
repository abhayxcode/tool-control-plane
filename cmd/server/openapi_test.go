package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/abhayxcode/tool-control-plane/internal/controlplane"
)

func TestOpenAPISpecDocumentsHTTPRoutes(t *testing.T) {
	spec, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatalf("read OpenAPI spec: %v", err)
	}
	text := string(spec)
	for _, expected := range []string{
		"/openapi.yaml:",
		"/healthz:",
		"/v1/capabilities:",
		"/v1/connectors:",
		"/v1/policies:",
		"/v1/readiness:",
		"/v1/tool-calls:",
		"/v1/tool-calls/{id}:",
		"/v1/audit:",
		"/v1/audit/export:",
		"/v1/approvals:",
		"/v1/approvals/{id}:",
		"/v1/approvals/{id}/grant:",
		"/v1/approvals/{id}/deny:",
		"/v1/approvals/{id}/execute:",
		"ToolCallRequest:",
		"ToolCallResponse:",
		"ToolCallRecord:",
		"ProviderRouteTrace:",
		"Connector:",
		"ConnectorCreateRequest:",
		"PolicyListResponse:",
		"PolicyRule:",
		"ProviderConfig:",
		"ReadinessResponse:",
		"ApprovalRequest:",
		"AuditEntry:",
		"AuditExportResponse:",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("OpenAPI spec missing %q", expected)
		}
	}
}

func TestOpenAPIEndpointServesSpec(t *testing.T) {
	mux := newMux(controlplane.NewService())
	req := httptest.NewRequest(http.MethodGet, "/openapi.yaml", nil)
	resp := httptest.NewRecorder()

	mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}
	if resp.Header().Get("Content-Type") != "application/yaml; charset=utf-8" {
		t.Fatalf("unexpected content type: %q", resp.Header().Get("Content-Type"))
	}
	body := resp.Body.String()
	if !strings.Contains(body, "openapi: 3.1.0") {
		t.Fatalf("expected OpenAPI document")
	}
	if !strings.Contains(body, "/v1/tool-calls:") {
		t.Fatalf("expected tool calls path")
	}
}
