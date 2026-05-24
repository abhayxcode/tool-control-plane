package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type Client struct {
	baseURL    string
	httpClient *http.Client
	token      string
}

type Option func(*Client)

func WithHTTPClient(httpClient *http.Client) Option {
	return func(client *Client) {
		if httpClient != nil {
			client.httpClient = httpClient
		}
	}
}

func WithBearerToken(token string) Option {
	return func(client *Client) {
		client.token = strings.TrimSpace(token)
	}
}

func New(baseURL string, options ...Option) (*Client, error) {
	baseURL = strings.TrimRight(baseURL, "/")
	if baseURL == "" {
		return nil, fmt.Errorf("base URL is required")
	}
	if _, err := url.ParseRequestURI(baseURL); err != nil {
		return nil, fmt.Errorf("invalid base URL: %w", err)
	}
	client := &Client{
		baseURL:    baseURL,
		httpClient: http.DefaultClient,
	}
	for _, option := range options {
		option(client)
	}
	return client, nil
}

func (c *Client) Health(ctx context.Context) (map[string]string, error) {
	var result map[string]string
	if err := c.do(ctx, http.MethodGet, "/healthz", nil, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) Capabilities(ctx context.Context) ([]string, []CapabilityDefinition, error) {
	var result struct {
		Capabilities []string               `json:"capabilities"`
		Details      []CapabilityDefinition `json:"details"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/capabilities", nil, &result); err != nil {
		return nil, nil, err
	}
	return result.Capabilities, result.Details, nil
}

func (c *Client) Connectors(ctx context.Context) ([]Connector, error) {
	var result struct {
		Connectors []Connector `json:"connectors"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/connectors", nil, &result); err != nil {
		return nil, err
	}
	return result.Connectors, nil
}

func (c *Client) CreateConnector(ctx context.Context, req ConnectorCreateRequest) (Connector, error) {
	var result Connector
	if err := c.do(ctx, http.MethodPost, "/v1/connectors", req, &result); err != nil {
		return Connector{}, err
	}
	return result, nil
}

func (c *Client) Policies(ctx context.Context) (PolicyListResponse, error) {
	var result PolicyListResponse
	if err := c.do(ctx, http.MethodGet, "/v1/policies", nil, &result); err != nil {
		return PolicyListResponse{}, err
	}
	return result, nil
}

func (c *Client) CallTool(ctx context.Context, req ToolCallRequest) (ToolCallResponse, error) {
	var result ToolCallResponse
	if err := c.do(ctx, http.MethodPost, "/v1/tool-calls", req, &result); err != nil {
		return ToolCallResponse{}, err
	}
	return result, nil
}

func (c *Client) ToolCalls(ctx context.Context) ([]ToolCallRecord, error) {
	var result struct {
		ToolCalls []ToolCallRecord `json:"tool_calls"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/tool-calls", nil, &result); err != nil {
		return nil, err
	}
	return result.ToolCalls, nil
}

func (c *Client) ToolCall(ctx context.Context, id string) (ToolCallRecord, error) {
	var result ToolCallRecord
	if err := c.do(ctx, http.MethodGet, "/v1/tool-calls/"+url.PathEscape(id), nil, &result); err != nil {
		return ToolCallRecord{}, err
	}
	return result, nil
}

func (c *Client) Audit(ctx context.Context) ([]AuditEntry, error) {
	var result struct {
		Entries []AuditEntry `json:"entries"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/audit", nil, &result); err != nil {
		return nil, err
	}
	return result.Entries, nil
}

func (c *Client) AuditExport(ctx context.Context) (AuditExportResponse, error) {
	var result AuditExportResponse
	if err := c.do(ctx, http.MethodGet, "/v1/audit/export", nil, &result); err != nil {
		return AuditExportResponse{}, err
	}
	return result, nil
}

func (c *Client) Approvals(ctx context.Context) ([]ApprovalRequest, error) {
	var result struct {
		Approvals []ApprovalRequest `json:"approvals"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/approvals", nil, &result); err != nil {
		return nil, err
	}
	return result.Approvals, nil
}

func (c *Client) Approval(ctx context.Context, id string) (ApprovalRequest, error) {
	var result ApprovalRequest
	if err := c.do(ctx, http.MethodGet, "/v1/approvals/"+url.PathEscape(id), nil, &result); err != nil {
		return ApprovalRequest{}, err
	}
	return result, nil
}

func (c *Client) GrantApproval(ctx context.Context, id string, req ApprovalDecisionRequest) (ApprovalDecisionResponse, error) {
	return c.decideApproval(ctx, id, "grant", req)
}

func (c *Client) DenyApproval(ctx context.Context, id string, req ApprovalDecisionRequest) (ApprovalDecisionResponse, error) {
	return c.decideApproval(ctx, id, "deny", req)
}

func (c *Client) ExecuteApproval(ctx context.Context, id string) (ApprovalExecuteResponse, error) {
	var result ApprovalExecuteResponse
	if err := c.do(ctx, http.MethodPost, "/v1/approvals/"+url.PathEscape(id)+"/execute", nil, &result); err != nil {
		return ApprovalExecuteResponse{}, err
	}
	return result, nil
}

func (c *Client) OpenAPI(ctx context.Context) ([]byte, error) {
	return c.doBytes(ctx, http.MethodGet, "/openapi.yaml", nil)
}

func (c *Client) decideApproval(ctx context.Context, id string, action string, req ApprovalDecisionRequest) (ApprovalDecisionResponse, error) {
	var result ApprovalDecisionResponse
	if err := c.do(ctx, http.MethodPost, "/v1/approvals/"+url.PathEscape(id)+"/"+action, req, &result); err != nil {
		return ApprovalDecisionResponse{}, err
	}
	return result, nil
}

func (c *Client) do(ctx context.Context, method string, path string, body any, target any) error {
	responseBody, err := c.doBytes(ctx, method, path, body)
	if err != nil {
		return err
	}
	if target == nil {
		return nil
	}
	if err := json.Unmarshal(responseBody, target); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func (c *Client) doBytes(ctx context.Context, method string, path string, body any) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	httpReq.Header.Set("Accept", "application/json")
	if c.token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.token)
	}

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()

	responseBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, err
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode > 299 {
		return nil, fmt.Errorf("%s %s failed with status %d: %s", method, path, httpResp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	return responseBody, nil
}
