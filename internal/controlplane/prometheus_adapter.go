package controlplane

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const PrometheusProvider = "prometheus"

var prometheusIdentifierPattern = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*$`)

type PrometheusAdapterConfig struct {
	BaseURL          string
	BearerToken      string
	ServiceLabel     string
	EnvironmentLabel string
	StatusLabel      string
	Client           *http.Client
}

type PrometheusAdapter struct {
	baseURL          string
	bearerToken      string
	serviceLabel     string
	environmentLabel string
	statusLabel      string
	client           *http.Client
}

func NewPrometheusAdapter(config PrometheusAdapterConfig) PrometheusAdapter {
	client := config.Client
	if client == nil {
		client = http.DefaultClient
	}
	return PrometheusAdapter{
		baseURL:          strings.TrimRight(strings.TrimSpace(config.BaseURL), "/"),
		bearerToken:      strings.TrimSpace(config.BearerToken),
		serviceLabel:     firstNonEmpty(config.ServiceLabel, "service"),
		environmentLabel: firstNonEmpty(config.EnvironmentLabel, "environment"),
		statusLabel:      firstNonEmpty(config.StatusLabel, "status"),
		client:           client,
	}
}

func (a PrometheusAdapter) Execute(definition CapabilityDefinition, req ToolCallRequest) (map[string]any, error) {
	if strings.TrimSpace(a.baseURL) == "" {
		return nil, errors.New("prometheus adapter requires PROMETHEUS_BASE_URL")
	}
	if definition.ID != "metrics.get_service_health" {
		return nil, fmt.Errorf("prometheus adapter does not support capability '%s'", definition.ID)
	}
	return a.getServiceHealth(req)
}

func (a PrometheusAdapter) getServiceHealth(req ToolCallRequest) (map[string]any, error) {
	args := req.Arguments
	service := firstNonEmpty(firstStringArg(args, "service", "service_id"), req.ServiceID)
	environment := firstNonEmpty(firstStringArg(args, "environment", "env"), req.Environment)
	window := firstNonEmpty(firstStringArg(args, "window", "range"), "5m")
	serviceLabel := firstNonEmpty(firstStringArg(args, "service_label"), a.serviceLabel)
	environmentLabel := firstNonEmpty(firstStringArg(args, "environment_label", "env_label"), a.environmentLabel)
	statusLabel := firstNonEmpty(firstStringArg(args, "status_label"), a.statusLabel)
	requestMetric := firstNonEmpty(firstStringArg(args, "request_metric"), "http_requests_total")
	durationBucketMetric := firstNonEmpty(firstStringArg(args, "duration_bucket_metric"), "http_request_duration_seconds_bucket")

	defaultQueries, err := a.defaultHealthQueries(prometheusHealthQueryConfig{
		Service:              service,
		Environment:          environment,
		Window:               window,
		ServiceLabel:         serviceLabel,
		EnvironmentLabel:     environmentLabel,
		StatusLabel:          statusLabel,
		RequestMetric:        requestMetric,
		DurationBucketMetric: durationBucketMetric,
	})
	if err != nil {
		return nil, err
	}

	queries := []prometheusHealthQuery{
		{Key: "up", Query: firstNonEmpty(firstStringArg(args, "up_query"), defaultQueries.Up)},
		{Key: "latency_p95_seconds", Query: firstNonEmpty(firstStringArg(args, "latency_p95_query", "latency_query"), defaultQueries.LatencyP95Seconds)},
		{Key: "error_rate_percent", Query: firstNonEmpty(firstStringArg(args, "error_rate_query"), defaultQueries.ErrorRatePercent)},
	}
	values := map[string]float64{}
	samples := map[string]int{}
	queryMap := map[string]string{}
	warnings := []string{}
	infos := []string{}
	for _, item := range queries {
		if strings.TrimSpace(item.Query) == "" {
			continue
		}
		queryMap[item.Key] = item.Query
		value, sampleCount, queryWarnings, queryInfos, err := a.queryScalar(item.Query)
		if err != nil {
			return nil, err
		}
		samples[item.Key] = sampleCount
		warnings = append(warnings, queryWarnings...)
		infos = append(infos, queryInfos...)
		if value != nil {
			values[item.Key] = *value
		}
	}

	latencyP95MS := 0.0
	if value, ok := values["latency_p95_seconds"]; ok {
		latencyP95MS = prometheusLatencyMillis(value, firstStringArg(args, "latency_unit"))
		values["latency_p95_ms"] = latencyP95MS
		delete(values, "latency_p95_seconds")
	}
	latencyThresholdMS := optionalFloatArg(args, "latency_p95_ms_threshold", 1000)
	errorRateThreshold := optionalFloatArg(args, "error_rate_percent_threshold", 5)
	upThreshold := optionalFloatArg(args, "up_threshold", 1)

	status := "unknown"
	observed := len(values)
	if observed > 0 {
		status = "healthy"
	}
	if up, ok := values["up"]; ok && up < upThreshold {
		status = "degraded"
	}
	if latency, ok := values["latency_p95_ms"]; ok && latency >= latencyThresholdMS {
		status = "degraded"
	}
	if errorRate, ok := values["error_rate_percent"]; ok && errorRate >= errorRateThreshold {
		status = "degraded"
	}

	result := map[string]any{
		"status":     status,
		"queries":    queryMap,
		"samples":    samples,
		"thresholds": map[string]any{"up": upThreshold, "latency_p95_ms": latencyThresholdMS, "error_rate_percent": errorRateThreshold},
		"source_url": a.graphURL(primaryPrometheusQuery(queryMap)),
		"evidence":   prometheusEvidence(service, environment, status, values),
	}
	for key, value := range values {
		result[key] = value
	}
	if len(warnings) > 0 {
		result["warnings"] = warnings
	}
	if len(infos) > 0 {
		result["infos"] = infos
	}
	return result, nil
}

type prometheusHealthQuery struct {
	Key   string
	Query string
}

type prometheusHealthQueries struct {
	Up                string
	LatencyP95Seconds string
	ErrorRatePercent  string
}

type prometheusHealthQueryConfig struct {
	Service              string
	Environment          string
	Window               string
	ServiceLabel         string
	EnvironmentLabel     string
	StatusLabel          string
	RequestMetric        string
	DurationBucketMetric string
}

func (a PrometheusAdapter) defaultHealthQueries(config prometheusHealthQueryConfig) (prometheusHealthQueries, error) {
	if err := validatePrometheusIdentifier("service_label", config.ServiceLabel); err != nil {
		return prometheusHealthQueries{}, err
	}
	if strings.TrimSpace(config.EnvironmentLabel) != "" {
		if err := validatePrometheusIdentifier("environment_label", config.EnvironmentLabel); err != nil {
			return prometheusHealthQueries{}, err
		}
	}
	if err := validatePrometheusIdentifier("status_label", config.StatusLabel); err != nil {
		return prometheusHealthQueries{}, err
	}
	if err := validatePrometheusIdentifier("request_metric", config.RequestMetric); err != nil {
		return prometheusHealthQueries{}, err
	}
	if err := validatePrometheusIdentifier("duration_bucket_metric", config.DurationBucketMetric); err != nil {
		return prometheusHealthQueries{}, err
	}
	baseSelector := prometheusMetricSelector("up", prometheusServiceMatchers(config.ServiceLabel, config.Service, config.EnvironmentLabel, config.Environment))
	requestSelector := prometheusMetricSelector(config.RequestMetric, prometheusServiceMatchers(config.ServiceLabel, config.Service, config.EnvironmentLabel, config.Environment))
	errorSelector := prometheusMetricSelector(config.RequestMetric, append(prometheusServiceMatchers(config.ServiceLabel, config.Service, config.EnvironmentLabel, config.Environment), fmt.Sprintf(`%s=~"5.."`, config.StatusLabel)))
	durationSelector := prometheusMetricSelector(config.DurationBucketMetric, prometheusServiceMatchers(config.ServiceLabel, config.Service, config.EnvironmentLabel, config.Environment))
	return prometheusHealthQueries{
		Up:                fmt.Sprintf("min(%s)", baseSelector),
		LatencyP95Seconds: fmt.Sprintf("histogram_quantile(0.95, sum(rate(%s[%s])) by (le))", durationSelector, config.Window),
		ErrorRatePercent:  fmt.Sprintf("100 * sum(rate(%s[%s])) / clamp_min(sum(rate(%s[%s])), 1)", errorSelector, config.Window, requestSelector, config.Window),
	}, nil
}

func (a PrometheusAdapter) queryScalar(query string) (*float64, int, []string, []string, error) {
	values := url.Values{}
	values.Set("query", query)
	requestURL := a.baseURL + "/api/v1/query?" + values.Encode()
	req, err := http.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, 0, nil, nil, err
	}
	if a.bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+a.bearerToken)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, 0, nil, nil, newPrometheusRequestError(http.MethodGet, requestURL, 0, nil, err)
	}
	defer resp.Body.Close()
	var body prometheusQueryResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, 0, nil, nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 || body.Status != "success" {
		message := body.Error
		if message == "" {
			message = resp.Status
		}
		return nil, 0, nil, nil, newPrometheusRequestError(http.MethodGet, requestURL, resp.StatusCode, []byte(message), nil)
	}
	value, sampleCount, err := prometheusQueryValue(body.Data.ResultType, body.Data.Result)
	if err != nil {
		return nil, 0, nil, nil, err
	}
	return value, sampleCount, body.Warnings, body.Infos, nil
}

type prometheusQueryResponse struct {
	Status    string `json:"status"`
	ErrorType string `json:"errorType,omitempty"`
	Error     string `json:"error,omitempty"`
	Data      struct {
		ResultType string          `json:"resultType"`
		Result     json.RawMessage `json:"result"`
	} `json:"data"`
	Warnings []string `json:"warnings,omitempty"`
	Infos    []string `json:"infos,omitempty"`
}

type prometheusVectorSample struct {
	Metric map[string]string `json:"metric"`
	Value  []json.RawMessage `json:"value"`
}

func prometheusQueryValue(resultType string, raw json.RawMessage) (*float64, int, error) {
	switch resultType {
	case "vector":
		var samples []prometheusVectorSample
		if err := json.Unmarshal(raw, &samples); err != nil {
			return nil, 0, err
		}
		if len(samples) == 0 {
			return nil, 0, nil
		}
		value, err := prometheusSampleValue(samples[0].Value)
		if err != nil {
			return nil, len(samples), err
		}
		return &value, len(samples), nil
	case "scalar":
		var sample []json.RawMessage
		if err := json.Unmarshal(raw, &sample); err != nil {
			return nil, 0, err
		}
		value, err := prometheusSampleValue(sample)
		if err != nil {
			return nil, 1, err
		}
		return &value, 1, nil
	default:
		return nil, 0, fmt.Errorf("prometheus query returned unsupported result type %q", resultType)
	}
}

func prometheusSampleValue(sample []json.RawMessage) (float64, error) {
	if len(sample) < 2 {
		return 0, fmt.Errorf("prometheus sample value is missing")
	}
	var text string
	if err := json.Unmarshal(sample[1], &text); err == nil {
		return strconv.ParseFloat(text, 64)
	}
	var numeric float64
	if err := json.Unmarshal(sample[1], &numeric); err == nil {
		return numeric, nil
	}
	return 0, fmt.Errorf("prometheus sample value must be numeric")
}

func prometheusMetricSelector(metric string, matchers []string) string {
	if len(matchers) == 0 {
		return metric
	}
	return metric + "{" + strings.Join(matchers, ",") + "}"
}

func prometheusServiceMatchers(serviceLabel string, service string, environmentLabel string, environment string) []string {
	matchers := []string{}
	if strings.TrimSpace(serviceLabel) != "" && strings.TrimSpace(service) != "" {
		matchers = append(matchers, fmt.Sprintf("%s=%s", serviceLabel, strconv.Quote(service)))
	}
	if strings.TrimSpace(environmentLabel) != "" && strings.TrimSpace(environment) != "" {
		matchers = append(matchers, fmt.Sprintf("%s=%s", environmentLabel, strconv.Quote(environment)))
	}
	return matchers
}

func validatePrometheusIdentifier(name string, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("prometheus %s must be set", name)
	}
	if !prometheusIdentifierPattern.MatchString(value) {
		return fmt.Errorf("prometheus %s %q is not a valid identifier", name, value)
	}
	return nil
}

func optionalFloatArg(args map[string]any, key string, fallback float64) float64 {
	value, ok := args[key]
	if !ok {
		return fallback
	}
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case json.Number:
		result, err := typed.Float64()
		if err == nil {
			return result
		}
	case string:
		result, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err == nil {
			return result
		}
	}
	return fallback
}

func prometheusLatencyMillis(value float64, unit string) float64 {
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "ms", "millisecond", "milliseconds":
		return value
	default:
		return value * 1000
	}
}

func primaryPrometheusQuery(queries map[string]string) string {
	for _, key := range []string{"up", "latency_p95_seconds", "error_rate_percent"} {
		if query := strings.TrimSpace(queries[key]); query != "" {
			return query
		}
	}
	return ""
}

func (a PrometheusAdapter) graphURL(query string) string {
	if strings.TrimSpace(query) == "" {
		return a.baseURL
	}
	values := url.Values{}
	values.Set("g0.expr", query)
	values.Set("g0.tab", "1")
	return a.baseURL + "/graph?" + values.Encode()
}

func prometheusEvidence(service string, environment string, status string, values map[string]float64) string {
	parts := []string{}
	if up, ok := values["up"]; ok {
		parts = append(parts, fmt.Sprintf("up=%.2f", up))
	}
	if latency, ok := values["latency_p95_ms"]; ok {
		parts = append(parts, fmt.Sprintf("p95 latency=%.2fms", latency))
	}
	if errorRate, ok := values["error_rate_percent"]; ok {
		parts = append(parts, fmt.Sprintf("error rate=%.2f%%", errorRate))
	}
	target := strings.TrimSpace(service + "/" + environment)
	if target == "/" {
		target = "requested service"
	}
	if len(parts) == 0 {
		return fmt.Sprintf("Prometheus returned no metric samples for %s.", target)
	}
	return fmt.Sprintf("Prometheus reports %s for %s: %s.", status, target, strings.Join(parts, ", "))
}

type prometheusRequestError struct {
	Method     string
	URL        string
	StatusCode int
	Body       string
	Cause      error
}

func newPrometheusRequestError(method string, requestURL string, statusCode int, body []byte, cause error) prometheusRequestError {
	return prometheusRequestError{
		Method:     method,
		URL:        requestURL,
		StatusCode: statusCode,
		Body:       strings.TrimSpace(string(body)),
		Cause:      cause,
	}
}

func (e prometheusRequestError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("prometheus API %s %s failed: %v", e.Method, e.URL, e.Cause)
	}
	if e.Body != "" {
		return fmt.Sprintf("prometheus API %s %s failed with status %d: %s", e.Method, e.URL, e.StatusCode, e.Body)
	}
	return fmt.Sprintf("prometheus API %s %s failed with status %d", e.Method, e.URL, e.StatusCode)
}

func (e prometheusRequestError) Unwrap() error {
	return e.Cause
}

func (e prometheusRequestError) ToolCallError() ToolCallError {
	return ToolCallError{
		Provider:   PrometheusProvider,
		Category:   "prometheus_api",
		Operation:  e.Method,
		StatusCode: e.StatusCode,
		Message:    e.Error(),
	}
}

func PrometheusProviderOverrides() map[string]string {
	return map[string]string{
		"metrics.get_service_health": PrometheusProvider,
	}
}
