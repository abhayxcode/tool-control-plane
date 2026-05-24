package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/abhayxcode/tool-control-plane/internal/controlplane"
)

const mcpProtocolVersion = "2025-06-18"

type mcpJSONRPCRequest struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method"`
	Params  json.RawMessage  `json:"params,omitempty"`
}

type mcpJSONRPCResponse struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Result  any              `json:"result,omitempty"`
	Error   *mcpJSONRPCError `json:"error,omitempty"`
}

type mcpJSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpToolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

type mcpResourceReadParams struct {
	URI string `json:"uri"`
}

func mcpHandler(svc *controlplane.Service, config Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req mcpJSONRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			nullID := json.RawMessage("null")
			writeMCPError(w, &nullID, -32700, "Parse error")
			return
		}
		if req.JSONRPC != "2.0" || strings.TrimSpace(req.Method) == "" {
			writeMCPError(w, req.ID, -32600, "Invalid Request")
			return
		}

		result, rpcErr := handleMCPRequest(svc, config, req, requestIDFromContext(r.Context()))
		if req.ID == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if rpcErr != nil {
			writeMCPError(w, req.ID, rpcErr.Code, rpcErr.Message)
			return
		}
		writeJSON(w, mcpJSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  result,
		})
	}
}

func handleMCPRequest(svc *controlplane.Service, config Config, req mcpJSONRPCRequest, httpRequestID string) (any, *mcpJSONRPCError) {
	switch req.Method {
	case "initialize":
		return map[string]any{
			"protocolVersion": mcpProtocolVersion,
			"capabilities": map[string]any{
				"tools":     map[string]any{"listChanged": false},
				"resources": map[string]any{"listChanged": false},
			},
			"serverInfo": map[string]any{
				"name":    "tool-control-plane",
				"version": "0.1.0",
			},
		}, nil
	case "ping", "notifications/initialized":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": mcpTools(svc)}, nil
	case "tools/call":
		return mcpCallTool(svc, req.Params, httpRequestID)
	case "resources/list":
		return map[string]any{"resources": mcpResources()}, nil
	case "resources/read":
		return mcpReadResource(svc, config, req.Params)
	default:
		return nil, &mcpJSONRPCError{Code: -32601, Message: "Method not found"}
	}
}

func mcpTools(svc *controlplane.Service) []map[string]any {
	details := svc.CapabilityDetails()
	tools := make([]map[string]any, 0, len(details))
	for _, detail := range details {
		tools = append(tools, map[string]any{
			"name":        detail.ID,
			"title":       mcpToolTitle(detail),
			"description": detail.Description,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"org_id":        map[string]any{"type": "string", "description": "Organization ID. Defaults to default."},
					"actor_user_id": map[string]any{"type": "string", "description": "User or agent actor ID. Defaults to mcp-client."},
					"agent_run_id":  map[string]any{"type": "string", "description": "Agent run ID. Generated when omitted."},
					"service_id":    map[string]any{"type": "string", "description": "Service identifier for audit and provider defaults."},
					"environment":   map[string]any{"type": "string", "description": "Environment for audit and provider defaults."},
					"request_id":    map[string]any{"type": "string", "description": "Optional caller request ID."},
					"arguments":     map[string]any{"type": "object", "description": "Provider-specific arguments.", "additionalProperties": true},
					"tool_args":     map[string]any{"type": "object", "description": "Alias for provider-specific arguments.", "additionalProperties": true},
				},
				"additionalProperties": true,
			},
			"annotations": map[string]any{
				"risk_level":        detail.RiskLevel,
				"provider":          detail.Provider,
				"approval_required": detail.ApprovalRequired,
			},
		})
	}
	return tools
}

func mcpToolTitle(detail controlplane.CapabilityDefinition) string {
	return strings.ReplaceAll(detail.ID, "_", " ")
}

func mcpCallTool(svc *controlplane.Service, rawParams json.RawMessage, httpRequestID string) (any, *mcpJSONRPCError) {
	var params mcpToolCallParams
	if err := decodeMCPParams(rawParams, &params); err != nil {
		return nil, &mcpJSONRPCError{Code: -32602, Message: "Invalid params"}
	}
	capability, action, ok := strings.Cut(strings.TrimSpace(params.Name), ".")
	if !ok || capability == "" || action == "" {
		return nil, &mcpJSONRPCError{Code: -32602, Message: "Tool name must use capability.action format"}
	}
	if err := validateMCPToolName(svc, params.Name); err != nil {
		return nil, &mcpJSONRPCError{Code: -32602, Message: err.Error()}
	}
	toolReq := mcpToolRequest(capability, action, params.Arguments, httpRequestID)
	toolResp := svc.CallTool(toolReq)
	text, _ := json.MarshalIndent(toolResp, "", "  ")
	result := map[string]any{
		"content": []map[string]any{
			{
				"type": "text",
				"text": string(text),
			},
		},
		"structuredContent": toolResp,
		"isError":           toolResp.Status != "success",
	}
	return result, nil
}

func mcpToolRequest(capability string, action string, args map[string]any, httpRequestID string) controlplane.ToolCallRequest {
	copied := map[string]any{}
	for key, value := range args {
		copied[key] = value
	}
	providerArgs := map[string]any{}
	if nested, ok := copied["arguments"].(map[string]any); ok {
		for key, value := range nested {
			providerArgs[key] = value
		}
		delete(copied, "arguments")
	}
	if nested, ok := copied["tool_args"].(map[string]any); ok {
		for key, value := range nested {
			providerArgs[key] = value
		}
		delete(copied, "tool_args")
	}
	for _, key := range []string{"org_id", "actor_user_id", "agent_run_id", "service_id", "environment", "request_id"} {
		delete(copied, key)
	}
	for key, value := range copied {
		providerArgs[key] = value
	}
	requestID := firstNonEmptyString(mcpStringArg(args, "request_id"), httpRequestID)
	return controlplane.ToolCallRequest{
		RequestID:   requestID,
		OrgID:       firstNonEmptyString(mcpStringArg(args, "org_id"), "default"),
		ActorUserID: firstNonEmptyString(mcpStringArg(args, "actor_user_id"), "mcp-client"),
		AgentRunID:  firstNonEmptyString(mcpStringArg(args, "agent_run_id"), "mcp-"+time.Now().UTC().Format("20060102T150405.000000000Z")),
		ServiceID:   mcpStringArg(args, "service_id"),
		Environment: mcpStringArg(args, "environment"),
		Capability:  capability,
		Action:      action,
		Arguments:   providerArgs,
	}
}

func mcpResources() []map[string]any {
	return []map[string]any{
		{
			"uri":         "tool-control-plane://capabilities",
			"name":        "capabilities",
			"title":       "Tool Control Plane Capabilities",
			"description": "Registered tool capabilities and provider metadata.",
			"mimeType":    "application/json",
		},
		{
			"uri":         "tool-control-plane://provider-config",
			"name":        "provider-config",
			"title":       "Tool Control Plane Provider Configuration",
			"description": "Non-secret provider readiness and routing configuration.",
			"mimeType":    "application/json",
		},
		{
			"uri":         "tool-control-plane://readiness",
			"name":        "readiness",
			"title":       "Tool Control Plane Readiness",
			"description": "Readiness checks, blockers, and non-secret dependency status.",
			"mimeType":    "application/json",
		},
		{
			"uri":         "tool-control-plane://audit",
			"name":        "audit",
			"title":       "Tool Control Plane Audit Log",
			"description": "Tool-call audit entries without raw provider outputs.",
			"mimeType":    "application/json",
		},
		{
			"uri":         "tool-control-plane://tool-calls",
			"name":        "tool-calls",
			"title":       "Tool Control Plane Tool Calls",
			"description": "Stored tool-call records with redacted arguments and results.",
			"mimeType":    "application/json",
		},
		{
			"uri":         "tool-control-plane://audit-export",
			"name":        "audit-export",
			"title":       "Tool Control Plane Audit Export",
			"description": "Governance export bundle with audit entries, tool-call records, and approvals.",
			"mimeType":    "application/json",
		},
	}
}

func mcpReadResource(svc *controlplane.Service, config Config, rawParams json.RawMessage) (any, *mcpJSONRPCError) {
	var params mcpResourceReadParams
	if err := decodeMCPParams(rawParams, &params); err != nil {
		return nil, &mcpJSONRPCError{Code: -32602, Message: "Invalid params"}
	}
	payload, ok := mcpResourcePayload(svc, config, strings.TrimSpace(params.URI))
	if !ok {
		return nil, &mcpJSONRPCError{Code: -32602, Message: "Unknown resource"}
	}
	text, _ := json.MarshalIndent(payload, "", "  ")
	return map[string]any{
		"contents": []map[string]any{
			{
				"uri":      params.URI,
				"mimeType": "application/json",
				"text":     string(text),
			},
		},
	}, nil
}

func mcpResourcePayload(svc *controlplane.Service, config Config, uri string) (any, bool) {
	switch uri {
	case "tool-control-plane://capabilities":
		return map[string]any{
			"capabilities": svc.Capabilities(),
			"details":      svc.CapabilityDetails(),
		}, true
	case "tool-control-plane://provider-config":
		return providerConfigSummary(config), true
	case "tool-control-plane://readiness":
		return readinessSummary(svc, config), true
	case "tool-control-plane://audit":
		return map[string]any{"entries": svc.Audit()}, true
	case "tool-control-plane://tool-calls":
		return map[string]any{"tool_calls": svc.ToolCalls()}, true
	case "tool-control-plane://audit-export":
		return auditExportPayload(svc), true
	default:
		return nil, false
	}
}

func decodeMCPParams(raw json.RawMessage, target any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return json.Unmarshal(raw, target)
}

func writeMCPError(w http.ResponseWriter, id *json.RawMessage, code int, message string) {
	writeJSON(w, mcpJSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &mcpJSONRPCError{Code: code, Message: message},
	})
}

func mcpStringArg(args map[string]any, key string) string {
	value, ok := args[key]
	if !ok {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func sortedMCPToolNames(svc *controlplane.Service) []string {
	names := append([]string{}, svc.Capabilities()...)
	sort.Strings(names)
	return names
}

func mcpToolNameSet(svc *controlplane.Service) map[string]bool {
	names := map[string]bool{}
	for _, name := range sortedMCPToolNames(svc) {
		names[name] = true
	}
	return names
}

func validateMCPToolName(svc *controlplane.Service, name string) error {
	if !mcpToolNameSet(svc)[name] {
		return fmt.Errorf("unknown tool: %s", name)
	}
	return nil
}
