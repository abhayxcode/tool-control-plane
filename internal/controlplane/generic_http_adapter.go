package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const GenericHTTPProvider = "generic_http"

const (
	defaultGenericHTTPTimeout          = 10 * time.Second
	defaultGenericHTTPMaxResponseBytes = 64 * 1024
	maxGenericHTTPResponseBytes        = 512 * 1024
)

type GenericHTTPAdapterConfig struct {
	BaseURL          string
	BearerToken      string
	AllowedMethods   []string
	Timeout          time.Duration
	MaxResponseBytes int
	Client           *http.Client
}

type GenericHTTPAdapter struct {
	config GenericHTTPAdapterConfig
}

func NewGenericHTTPAdapter(config GenericHTTPAdapterConfig) GenericHTTPAdapter {
	if config.Timeout <= 0 {
		config.Timeout = defaultGenericHTTPTimeout
	}
	if config.MaxResponseBytes <= 0 {
		config.MaxResponseBytes = defaultGenericHTTPMaxResponseBytes
	}
	if config.MaxResponseBytes > maxGenericHTTPResponseBytes {
		config.MaxResponseBytes = maxGenericHTTPResponseBytes
	}
	if len(config.AllowedMethods) == 0 {
		config.AllowedMethods = []string{http.MethodGet}
	}
	return GenericHTTPAdapter{config: config}
}

func (a GenericHTTPAdapter) Execute(definition CapabilityDefinition, req ToolCallRequest) (map[string]any, error) {
	if definition.ID != "internal_api.request" {
		return nil, fmt.Errorf("generic http adapter does not support tool '%s'", definition.ID)
	}
	baseURL := strings.TrimSpace(a.config.BaseURL)
	if baseURL == "" {
		return nil, fmt.Errorf("generic http base URL is required")
	}
	targetURL, err := genericHTTPURL(baseURL, req.Arguments)
	if err != nil {
		return nil, err
	}
	method := genericHTTPMethod(req.Arguments)
	if !genericHTTPMethodAllowed(method, a.config.AllowedMethods) {
		return nil, fmt.Errorf("generic http method %s is not allowed", method)
	}
	bodyReader, contentType, err := genericHTTPBody(method, req.Arguments)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), a.config.Timeout)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, method, targetURL.String(), bodyReader)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		httpReq.Header.Set("Content-Type", contentType)
	}
	httpReq.Header.Set("Accept", "application/json, text/plain;q=0.9, */*;q=0.1")
	if strings.TrimSpace(a.config.BearerToken) != "" {
		httpReq.Header.Set("Authorization", "Bearer "+strings.TrimSpace(a.config.BearerToken))
	}
	if err := genericHTTPApplyHeaders(httpReq.Header, req.Arguments); err != nil {
		return nil, err
	}

	client := a.config.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, genericHTTPRequestError{
			method:    method,
			url:       targetURL.String(),
			retryable: true,
			message:   err.Error(),
		}
	}
	defer resp.Body.Close()

	body, truncated, err := genericHTTPReadBody(resp.Body, a.config.MaxResponseBytes)
	if err != nil {
		return nil, genericHTTPRequestError{
			method:     method,
			url:        targetURL.String(),
			statusCode: resp.StatusCode,
			retryable:  genericHTTPRetryable(resp.StatusCode),
			message:    err.Error(),
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = resp.Status
		}
		return nil, genericHTTPRequestError{
			method:     method,
			url:        targetURL.String(),
			statusCode: resp.StatusCode,
			retryable:  genericHTTPRetryable(resp.StatusCode),
			message:    message,
		}
	}

	result := map[string]any{
		"status_code":        resp.StatusCode,
		"ok":                 true,
		"method":             method,
		"source_url":         targetURL.String(),
		"content_type":       resp.Header.Get("Content-Type"),
		"response_truncated": truncated,
		"evidence":           fmt.Sprintf("Generic HTTP adapter called %s %s and received HTTP %d.", method, targetURL.String(), resp.StatusCode),
	}
	if parsed, ok := genericHTTPJSONBody(resp.Header.Get("Content-Type"), body); ok {
		result["body_json"] = parsed
	} else {
		result["body_text"] = string(body)
	}
	return result, nil
}

func GenericHTTPProviderOverrides() map[string]string {
	return map[string]string{
		"internal_api.request": GenericHTTPProvider,
	}
}

func genericHTTPURL(base string, args map[string]any) (*url.URL, error) {
	baseURL, err := url.Parse(base)
	if err != nil || baseURL.Scheme == "" || baseURL.Host == "" {
		return nil, fmt.Errorf("generic http base URL must include scheme and host")
	}
	rawPath, ok := stringArg(args, "path")
	if !ok {
		return nil, fmt.Errorf("internal_api.request requires path argument")
	}
	relative, err := url.Parse(strings.TrimSpace(rawPath))
	if err != nil {
		return nil, fmt.Errorf("generic http path is invalid: %w", err)
	}
	if relative.IsAbs() || relative.Host != "" || strings.HasPrefix(strings.TrimSpace(rawPath), "//") {
		return nil, fmt.Errorf("generic http path must be relative to configured base URL")
	}
	if genericHTTPPathHasTraversal(relative.Path) {
		return nil, fmt.Errorf("generic http path must not contain '..' segments")
	}
	target := baseURL.ResolveReference(relative)
	query, err := genericHTTPQuery(args)
	if err != nil {
		return nil, err
	}
	if len(query) > 0 {
		values := target.Query()
		for key, items := range query {
			for _, item := range items {
				values.Add(key, item)
			}
		}
		target.RawQuery = values.Encode()
	}
	return target, nil
}

func genericHTTPMethod(args map[string]any) string {
	method, ok := stringArg(args, "method")
	if !ok {
		return http.MethodGet
	}
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" {
		return http.MethodGet
	}
	return method
}

func genericHTTPMethodAllowed(method string, allowed []string) bool {
	for _, item := range allowed {
		if strings.EqualFold(strings.TrimSpace(item), method) {
			return true
		}
	}
	return false
}

func genericHTTPQuery(args map[string]any) (url.Values, error) {
	raw, ok := args["query"]
	if !ok {
		return nil, nil
	}
	values := url.Values{}
	switch query := raw.(type) {
	case map[string]any:
		for key, value := range query {
			if strings.TrimSpace(key) == "" {
				return nil, fmt.Errorf("generic http query keys must be non-empty")
			}
			switch typed := value.(type) {
			case string:
				values.Add(key, typed)
			case float64, bool, int:
				values.Add(key, fmt.Sprint(typed))
			case []any:
				for _, item := range typed {
					values.Add(key, fmt.Sprint(item))
				}
			case []string:
				for _, item := range typed {
					values.Add(key, item)
				}
			default:
				return nil, fmt.Errorf("generic http query values must be scalars or arrays")
			}
		}
	default:
		return nil, fmt.Errorf("generic http query must be an object")
	}
	return values, nil
}

func genericHTTPBody(method string, args map[string]any) (io.Reader, string, error) {
	raw, ok := args["body"]
	if !ok || raw == nil {
		return nil, "", nil
	}
	if method == http.MethodGet || method == http.MethodHead {
		return nil, "", fmt.Errorf("generic http %s requests cannot include body", method)
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, "", fmt.Errorf("encode generic http body: %w", err)
	}
	return bytes.NewReader(data), "application/json", nil
}

func genericHTTPApplyHeaders(headers http.Header, args map[string]any) error {
	raw, ok := args["headers"]
	if !ok {
		return nil
	}
	items, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("generic http headers must be an object")
	}
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if sensitiveToolCallKey(key) {
			return fmt.Errorf("generic http header %q is not allowed in tool arguments", key)
		}
		value, ok := items[key].(string)
		if !ok {
			return fmt.Errorf("generic http header %q must be a string", key)
		}
		headers.Set(key, value)
	}
	return nil
}

func genericHTTPReadBody(body io.Reader, limit int) ([]byte, bool, error) {
	if limit <= 0 {
		limit = defaultGenericHTTPMaxResponseBytes
	}
	data, err := io.ReadAll(io.LimitReader(body, int64(limit)+1))
	if err != nil {
		return nil, false, err
	}
	if len(data) > limit {
		return data[:limit], true, nil
	}
	return data, false, nil
}

func genericHTTPJSONBody(contentType string, body []byte) (any, bool) {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.Contains(mediaType, "json") {
		return nil, false
	}
	var result any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, false
	}
	return result, true
}

func genericHTTPPathHasTraversal(path string) bool {
	for _, segment := range strings.Split(path, "/") {
		if segment == ".." {
			return true
		}
	}
	return false
}

func genericHTTPRetryable(statusCode int) bool {
	return statusCode == http.StatusRequestTimeout || statusCode == http.StatusTooManyRequests || statusCode >= 500
}

type genericHTTPRequestError struct {
	method     string
	url        string
	statusCode int
	retryable  bool
	message    string
}

func (e genericHTTPRequestError) Error() string {
	if e.statusCode > 0 {
		return fmt.Sprintf("generic http %s %s failed with status %d: %s", e.method, e.url, e.statusCode, e.message)
	}
	return fmt.Sprintf("generic http %s %s failed: %s", e.method, e.url, e.message)
}

func (e genericHTTPRequestError) ToolCallError() ToolCallError {
	return ToolCallError{
		Provider:   GenericHTTPProvider,
		Category:   "provider_error",
		Operation:  "internal_api.request",
		StatusCode: e.statusCode,
		Attempts:   1,
		Retryable:  e.retryable,
		Message:    e.message,
	}
}
