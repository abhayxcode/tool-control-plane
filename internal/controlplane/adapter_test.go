package controlplane

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAdapterRegistryExecutesProviderAdapter(t *testing.T) {
	registry := NewAdapterRegistry(map[string]ToolAdapter{
		"mock": NewMockAdapter(map[string]map[string]any{
			"metrics.get_service_health": {
				"status": "ok",
			},
		}),
	})

	response := registry.Execute(CapabilityDefinition{
		ID:         "metrics.get_service_health",
		RiskLevel:  RiskRead,
		Provider:   "mock",
		Capability: "metrics",
		Action:     "get_service_health",
	}, ToolCallRequest{
		ServiceID:   "backend",
		Environment: "prod",
	})

	if response.Status != "success" {
		t.Fatalf("expected success, got %q", response.Status)
	}
	if response.Provider != "mock" {
		t.Fatalf("expected mock provider, got %q", response.Provider)
	}
	if response.Result["status"] != "ok" {
		t.Fatalf("expected fixture result")
	}
}

func TestAdapterRegistryReturnsErrorForMissingProvider(t *testing.T) {
	registry := NewAdapterRegistry(map[string]ToolAdapter{})
	response := registry.Execute(CapabilityDefinition{
		ID:        "metrics.get_service_health",
		RiskLevel: RiskRead,
		Provider:  "missing",
	}, ToolCallRequest{})

	if response.Status != "error" {
		t.Fatalf("expected error, got %q", response.Status)
	}
	if response.Reason == "" {
		t.Fatalf("expected error reason")
	}
}

func TestMockAdapterReturnsErrorForMissingFixture(t *testing.T) {
	adapter := NewMockAdapter(map[string]map[string]any{})
	_, err := adapter.Execute(CapabilityDefinition{
		ID: "metrics.get_service_health",
	}, ToolCallRequest{})
	if err == nil {
		t.Fatalf("expected missing fixture error")
	}
}

func TestGitHubAdapterRequiresToken(t *testing.T) {
	adapter := NewGitHubAdapter(GitHubAdapterConfig{})
	_, err := adapter.Execute(CapabilityDefinition{
		ID:       "ci.get_checks",
		Provider: GitHubProvider,
	}, ToolCallRequest{})
	if err == nil {
		t.Fatalf("expected missing token error")
	}
}

func TestGitHubAdapterUsesGitHubAppInstallationToken(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	privateKeyPEM := testGitHubAppPrivateKeyPEM(t)
	var tokenRequests int
	var apiRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && r.URL.Path == "/app/installations/42/access_tokens" {
			tokenRequests++
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, "Bearer ") {
				t.Fatalf("expected bearer JWT auth, got %q", auth)
			}
			assertJWTIssuer(t, strings.TrimPrefix(auth, "Bearer "), "12345")
			w.Write([]byte(`{"token":"installation-token","expires_at":"2026-07-16T13:00:00Z"}`))
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/repos/acme/backend/commits/main/check-runs" {
			apiRequests++
			if r.Header.Get("Authorization") != "Bearer installation-token" {
				t.Fatalf("expected installation token auth, got %q", r.Header.Get("Authorization"))
			}
			w.Write([]byte(`{"check_runs":[]}`))
			return
		}
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	source, err := NewGitHubAppTokenSource(GitHubAppTokenSourceConfig{
		AppID:          "12345",
		InstallationID: "42",
		PrivateKeyPEM:  privateKeyPEM,
		BaseURL:        server.URL,
		Client:         server.Client(),
		Now:            func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("new github app token source: %v", err)
	}
	adapter := NewGitHubAdapter(GitHubAdapterConfig{
		TokenSource: source,
		BaseURL:     server.URL,
		Client:      server.Client(),
	})
	for i := 0; i < 2; i++ {
		result, err := adapter.Execute(CapabilityDefinition{
			ID:       "ci.get_checks",
			Provider: GitHubProvider,
		}, ToolCallRequest{
			Arguments: map[string]any{
				"repository": "acme/backend",
				"ref":        "main",
			},
		})
		if err != nil {
			t.Fatalf("expected app-auth checks result, got error: %v", err)
		}
		if result["status"] != "pending" {
			t.Fatalf("unexpected status: %#v", result["status"])
		}
	}
	if tokenRequests != 1 {
		t.Fatalf("expected cached installation token, got %d token requests", tokenRequests)
	}
	if apiRequests != 2 {
		t.Fatalf("expected two API requests, got %d", apiRequests)
	}
}

func TestSentryAdapterGetsRecentErrors(t *testing.T) {
	var sawAuth bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/0/projects/acme/backend/issues/" {
			t.Fatalf("unexpected sentry request: %s %s", r.Method, r.URL.String())
		}
		if r.Header.Get("Authorization") == "Bearer sentry-token" {
			sawAuth = true
		}
		if r.URL.Query().Get("query") != "is:unresolved environment:prod" {
			t.Fatalf("unexpected query: %q", r.URL.Query().Get("query"))
		}
		if r.URL.Query().Get("sort") != "freq" {
			t.Fatalf("unexpected sort: %q", r.URL.Query().Get("sort"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{
			"id": "1",
			"shortId": "BACKEND-1",
			"title": "database connection timeout",
			"culprit": "db.connect",
			"level": "error",
			"status": "unresolved",
			"count": "431",
			"userCount": 42,
			"firstSeen": "2026-07-16T09:00:00Z",
			"lastSeen": "2026-07-16T10:00:00Z",
			"permalink": "https://sentry.example/acme/backend/issues/1/"
		}]`))
	}))
	defer server.Close()

	adapter := NewSentryAdapter(SentryAdapterConfig{
		Token:   "sentry-token",
		Org:     "acme",
		Project: "backend",
		BaseURL: server.URL,
		Client:  server.Client(),
	})
	result, err := adapter.Execute(CapabilityDefinition{
		ID:       "errors.get_recent_errors",
		Provider: SentryProvider,
	}, ToolCallRequest{
		Environment: "prod",
		Arguments: map[string]any{
			"environment": "prod",
		},
	})
	if err != nil {
		t.Fatalf("expected sentry result, got error: %v", err)
	}
	if !sawAuth {
		t.Fatalf("expected sentry bearer token")
	}
	if result["status"] != "degraded" {
		t.Fatalf("expected degraded status, got %#v", result["status"])
	}
	topErrors := result["top_errors"].([]map[string]any)
	if topErrors[0]["title"] != "database connection timeout" {
		t.Fatalf("unexpected top error: %#v", topErrors[0])
	}
	if result["source_url"] != "https://sentry.example/acme/backend/issues/1/" {
		t.Fatalf("unexpected source url: %#v", result["source_url"])
	}
}

func TestSentryAdapterReturnsHealthyWhenNoIssues(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[]`))
	}))
	defer server.Close()

	adapter := NewSentryAdapter(SentryAdapterConfig{
		Token:   "sentry-token",
		BaseURL: server.URL,
		Client:  server.Client(),
	})
	result, err := adapter.Execute(CapabilityDefinition{
		ID:       "errors.get_recent_errors",
		Provider: SentryProvider,
	}, ToolCallRequest{
		Arguments: map[string]any{
			"organization": "acme",
			"project":      "backend",
			"query":        "",
		},
	})
	if err != nil {
		t.Fatalf("expected sentry result, got error: %v", err)
	}
	if result["status"] != "healthy" {
		t.Fatalf("expected healthy status, got %#v", result["status"])
	}
	if result["source_url"] != "" {
		t.Fatalf("expected no source url, got %#v", result["source_url"])
	}
}

func TestPrometheusAdapterGetsServiceHealth(t *testing.T) {
	requestsByQuery := map[string]int{}
	var authCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/query" {
			t.Fatalf("unexpected prometheus request: %s %s", r.Method, r.URL.String())
		}
		if r.Header.Get("Authorization") == "Bearer prom-token" {
			authCount++
		}
		query := r.URL.Query().Get("query")
		requestsByQuery[query]++
		value := ""
		switch {
		case strings.HasPrefix(query, "min(up"):
			if !strings.Contains(query, `service="backend"`) || !strings.Contains(query, `environment="prod"`) {
				t.Fatalf("expected service/environment selectors, got %q", query)
			}
			value = "1"
		case strings.Contains(query, "histogram_quantile"):
			value = "1.234"
		case strings.Contains(query, "http_requests_total"):
			if !strings.Contains(query, `status=~"5.."`) {
				t.Fatalf("expected 5xx status matcher, got %q", query)
			}
			value = "6.2"
		default:
			t.Fatalf("unexpected query: %q", query)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[123,"%s"]}]}}`, value)
	}))
	defer server.Close()

	adapter := NewPrometheusAdapter(PrometheusAdapterConfig{
		BaseURL:     server.URL,
		BearerToken: "prom-token",
		Client:      server.Client(),
	})
	result, err := adapter.Execute(CapabilityDefinition{
		ID:       "metrics.get_service_health",
		Provider: PrometheusProvider,
	}, ToolCallRequest{
		ServiceID:   "backend",
		Environment: "prod",
	})
	if err != nil {
		t.Fatalf("expected prometheus result, got error: %v", err)
	}
	if authCount != 3 {
		t.Fatalf("expected bearer token on three requests, got %d", authCount)
	}
	if len(requestsByQuery) != 3 {
		t.Fatalf("expected three queries, got %#v", requestsByQuery)
	}
	if result["status"] != "degraded" {
		t.Fatalf("expected degraded status, got %#v", result["status"])
	}
	if result["up"] != float64(1) {
		t.Fatalf("unexpected up value: %#v", result["up"])
	}
	if result["latency_p95_ms"] != float64(1234) {
		t.Fatalf("unexpected latency value: %#v", result["latency_p95_ms"])
	}
	if result["error_rate_percent"] != 6.2 {
		t.Fatalf("unexpected error rate value: %#v", result["error_rate_percent"])
	}
	if result["source_url"] == "" {
		t.Fatalf("expected source url")
	}
}

func TestPrometheusAdapterReturnsUnknownWhenNoSamples(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
	defer server.Close()

	adapter := NewPrometheusAdapter(PrometheusAdapterConfig{
		BaseURL: server.URL,
		Client:  server.Client(),
	})
	result, err := adapter.Execute(CapabilityDefinition{
		ID:       "metrics.get_service_health",
		Provider: PrometheusProvider,
	}, ToolCallRequest{
		ServiceID:   "backend",
		Environment: "prod",
	})
	if err != nil {
		t.Fatalf("expected prometheus result, got error: %v", err)
	}
	if result["status"] != "unknown" {
		t.Fatalf("expected unknown status, got %#v", result["status"])
	}
	if strings.Contains(result["evidence"].(string), "returned no metric samples") == false {
		t.Fatalf("unexpected evidence: %#v", result["evidence"])
	}
}

func TestKubernetesAdapterGetsWorkloadStatus(t *testing.T) {
	requestsByPath := map[string]int{}
	var authCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestsByPath[r.URL.Path]++
		if r.Header.Get("Authorization") == "Bearer kube-token" {
			authCount++
		}
		switch r.URL.Path {
		case "/api/v1/namespaces/prod/pods":
			if r.URL.Query().Get("labelSelector") != "app=backend-api" {
				t.Fatalf("unexpected label selector: %q", r.URL.Query().Get("labelSelector"))
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"items":[
				{
					"metadata":{"name":"backend-api-good","namespace":"prod"},
					"spec":{"nodeName":"node-a"},
					"status":{
						"phase":"Running",
						"podIP":"10.0.0.1",
						"hostIP":"10.0.1.1",
						"startTime":"2026-07-16T09:00:00Z",
						"conditions":[{"type":"Ready","status":"True"}],
						"containerStatuses":[{"name":"app","ready":true,"restartCount":0,"state":{"running":{"startedAt":"2026-07-16T09:00:00Z"}}}]
					}
				},
				{
					"metadata":{"name":"backend-api-bad","namespace":"prod"},
					"spec":{"nodeName":"node-b"},
					"status":{
						"phase":"Running",
						"podIP":"10.0.0.2",
						"hostIP":"10.0.1.2",
						"conditions":[{"type":"Ready","status":"False","reason":"ContainersNotReady"}],
						"containerStatuses":[{
							"name":"app",
							"ready":false,
							"restartCount":3,
							"state":{"waiting":{"reason":"CrashLoopBackOff","message":"back-off restarting failed container"}}
						}]
					}
				}
			]}`))
		case "/api/v1/namespaces/prod/events":
			if r.URL.Query().Get("fieldSelector") != "involvedObject.kind=Pod" {
				t.Fatalf("unexpected field selector: %q", r.URL.Query().Get("fieldSelector"))
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"items":[
				{
					"metadata":{"name":"ev1","creationTimestamp":"2026-07-16T09:10:00Z"},
					"involvedObject":{"kind":"Pod","name":"backend-api-bad"},
					"type":"Warning",
					"reason":"BackOff",
					"message":"Back-off restarting failed container",
					"count":4,
					"lastTimestamp":"2026-07-16T09:11:00Z"
				}
			]}`))
		case "/api/v1/namespaces/prod/pods/backend-api-bad/log":
			if r.URL.Query().Get("tailLines") != "50" {
				t.Fatalf("unexpected tail lines: %q", r.URL.Query().Get("tailLines"))
			}
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte("2026-07-16T09:11:00Z panic: database connection refused\n"))
		default:
			t.Fatalf("unexpected kubernetes request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	adapter := NewKubernetesAdapter(KubernetesAdapterConfig{
		BaseURL:     server.URL,
		BearerToken: "kube-token",
		Client:      server.Client(),
	})
	result, err := adapter.Execute(CapabilityDefinition{
		ID:       "runtime.get_workload_status",
		Provider: KubernetesProvider,
	}, ToolCallRequest{
		ServiceID:   "backend",
		Environment: "prod",
		Arguments: map[string]any{
			"namespace": "prod",
			"workload":  "backend-api",
		},
	})
	if err != nil {
		t.Fatalf("expected kubernetes result, got error: %v", err)
	}
	if authCount != 3 {
		t.Fatalf("expected bearer token on three requests, got %d", authCount)
	}
	if requestsByPath["/api/v1/namespaces/prod/pods"] != 1 || requestsByPath["/api/v1/namespaces/prod/events"] != 1 || requestsByPath["/api/v1/namespaces/prod/pods/backend-api-bad/log"] != 1 {
		t.Fatalf("unexpected requests: %#v", requestsByPath)
	}
	if result["status"] != "degraded" {
		t.Fatalf("expected degraded status, got %#v", result["status"])
	}
	if result["pods_ready"] != "1/2" {
		t.Fatalf("expected 1/2 pods ready, got %#v", result["pods_ready"])
	}
	if result["restart_count"] != 3 {
		t.Fatalf("expected restart count, got %#v", result["restart_count"])
	}
	if result["warning_event_count"] != 1 {
		t.Fatalf("expected warning event count, got %#v", result["warning_event_count"])
	}
	logs := result["logs"].([]map[string]any)
	if logs[0]["pod"] != "backend-api-bad" || !strings.Contains(logs[0]["log_excerpt"].(string), "database connection refused") {
		t.Fatalf("unexpected logs: %#v", logs)
	}
}

func TestKubernetesAdapterReturnsUnknownWhenNoPods(t *testing.T) {
	var eventRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/namespaces/default/events" {
			eventRequests++
		}
		if r.URL.Path != "/api/v1/namespaces/default/pods" {
			t.Fatalf("unexpected kubernetes request: %s", r.URL.String())
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"items":[]}`))
	}))
	defer server.Close()

	adapter := NewKubernetesAdapter(KubernetesAdapterConfig{
		BaseURL: server.URL,
		Client:  server.Client(),
	})
	result, err := adapter.Execute(CapabilityDefinition{
		ID:       "runtime.get_workload_status",
		Provider: KubernetesProvider,
	}, ToolCallRequest{
		ServiceID: "backend",
	})
	if err != nil {
		t.Fatalf("expected kubernetes result, got error: %v", err)
	}
	if result["status"] != "unknown" {
		t.Fatalf("expected unknown status, got %#v", result["status"])
	}
	if eventRequests != 0 {
		t.Fatalf("expected no event request when there are no pods")
	}
}

func TestGitHubAdapterRejectsUnsupportedCapabilities(t *testing.T) {
	adapter := NewGitHubAdapter(GitHubAdapterConfig{
		Token: "test-token",
	})
	_, err := adapter.Execute(CapabilityDefinition{
		ID:       "deploy.rollback",
		Provider: GitHubProvider,
	}, ToolCallRequest{})
	if err == nil {
		t.Fatalf("expected unsupported capability error")
	}
	if err.Error() != "github adapter does not support capability 'deploy.rollback'" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGitHubAdapterRetriesRetryableReadRequests(t *testing.T) {
	var attempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if r.URL.Path != "/repos/acme/backend/commits/main/check-runs" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if attempts == 1 {
			http.Error(w, "temporary github outage", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"check_runs":[]}`))
	}))
	defer server.Close()

	adapter := NewGitHubAdapter(GitHubAdapterConfig{
		Token:        "test-token",
		BaseURL:      server.URL,
		MaxAttempts:  3,
		RetryBackoff: 0,
	})
	result, err := adapter.Execute(CapabilityDefinition{
		ID:       "ci.get_checks",
		Provider: GitHubProvider,
	}, ToolCallRequest{
		Arguments: map[string]any{
			"repository": "acme/backend",
			"ref":        "main",
		},
	})
	if err != nil {
		t.Fatalf("expected retry success, got %v", err)
	}
	if attempts != 2 {
		t.Fatalf("expected two attempts, got %d", attempts)
	}
	if result["status"] != "pending" {
		t.Fatalf("unexpected status: %#v", result["status"])
	}
}

func TestGitHubAdapterReturnsRetryMetadataOnProviderError(t *testing.T) {
	var attempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		http.Error(w, "temporary github outage", http.StatusBadGateway)
	}))
	defer server.Close()

	registry := NewAdapterRegistry(map[string]ToolAdapter{
		GitHubProvider: NewGitHubAdapter(GitHubAdapterConfig{
			Token:        "test-token",
			BaseURL:      server.URL,
			MaxAttempts:  2,
			RetryBackoff: 0,
		}),
	})
	response := registry.Execute(CapabilityDefinition{
		ID:         "ci.get_checks",
		RiskLevel:  RiskRead,
		Provider:   GitHubProvider,
		Capability: "ci",
		Action:     "get_checks",
	}, ToolCallRequest{
		Arguments: map[string]any{
			"repository": "acme/backend",
			"ref":        "main",
		},
	})

	if response.Status != "error" {
		t.Fatalf("expected error, got %q", response.Status)
	}
	if attempts != 2 {
		t.Fatalf("expected two attempts, got %d", attempts)
	}
	if response.Error == nil {
		t.Fatalf("expected provider error metadata")
	}
	if response.Error.Provider != GitHubProvider || response.Error.Category != "github_api" {
		t.Fatalf("unexpected provider error: %#v", response.Error)
	}
	if response.Error.StatusCode != http.StatusBadGateway || response.Error.Attempts != 2 || !response.Error.Retryable {
		t.Fatalf("unexpected retry metadata: %#v", response.Error)
	}
}

func TestGitHubAdapterDoesNotRetryWriteRequests(t *testing.T) {
	var attempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		http.Error(w, "temporary github outage", http.StatusBadGateway)
	}))
	defer server.Close()

	adapter := NewGitHubAdapter(GitHubAdapterConfig{
		Token:        "test-token",
		BaseURL:      server.URL,
		MaxAttempts:  3,
		RetryBackoff: 0,
	})
	_, err := adapter.Execute(CapabilityDefinition{
		ID:       "code_host.create_draft_pr",
		Provider: GitHubProvider,
	}, ToolCallRequest{
		Arguments: map[string]any{
			"repository": "acme/backend",
			"title":      "Draft: test",
			"head":       "majdoor/test",
			"base":       "main",
		},
	})
	if err == nil {
		t.Fatalf("expected write failure")
	}
	if attempts != 1 {
		t.Fatalf("expected one write attempt, got %d", attempts)
	}
}

func TestGitHubAdapterCreatesDraftPR(t *testing.T) {
	var sawAuth bool
	var sawReviewers bool
	var sawLabels bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if r.Header.Get("Authorization") == "Bearer test-token" {
			sawAuth = true
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if r.URL.Path == "/repos/acme/backend/pulls/999/requested_reviewers" {
			sawReviewers = true
			assertStringSlice(t, payload["reviewers"], []string{"octocat"})
			assertStringSlice(t, payload["team_reviewers"], []string{"platform"})
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"ok":true}`))
			return
		}
		if r.URL.Path == "/repos/acme/backend/issues/999/labels" {
			sawLabels = true
			assertStringSlice(t, payload["labels"], []string{"majdoor", "backend"})
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"ok":true}`))
			return
		}
		if r.URL.Path != "/repos/acme/backend/pulls" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if payload["title"] != "Draft: Revert backend database pool config" {
			t.Fatalf("unexpected title: %#v", payload["title"])
		}
		if payload["head"] != "majdoor/revert-db-pool-config" {
			t.Fatalf("unexpected head branch: %#v", payload["head"])
		}
		if payload["base"] != "main" {
			t.Fatalf("unexpected base branch: %#v", payload["base"])
		}
		if payload["draft"] != true {
			t.Fatalf("expected draft PR")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{
			"number": 999,
			"title": "Draft: Revert backend database pool config",
			"html_url": "https://github.com/acme/backend/pull/999",
			"draft": true,
			"head": {
				"ref": "majdoor/revert-db-pool-config",
				"sha": "head-sha-999"
			}
		}`))
	}))
	defer server.Close()

	adapter := NewGitHubAdapter(GitHubAdapterConfig{
		Token:   "test-token",
		BaseURL: server.URL,
		Client:  server.Client(),
	})
	result, err := adapter.Execute(CapabilityDefinition{
		ID:       "code_host.create_draft_pr",
		Provider: GitHubProvider,
	}, ToolCallRequest{
		Arguments: map[string]any{
			"repository":     "acme/backend",
			"title":          "Draft: Revert backend database pool config",
			"branch":         "majdoor/revert-db-pool-config",
			"body":           "Validated patch artifact attached to agent run.",
			"reviewers":      []any{"octocat"},
			"team_reviewers": []any{"platform"},
			"labels":         []any{"majdoor", "backend"},
		},
	})
	if err != nil {
		t.Fatalf("expected draft PR result, got error: %v", err)
	}
	if !sawAuth {
		t.Fatalf("expected authorization header")
	}
	if !sawReviewers || !sawLabels {
		t.Fatalf("expected reviewers and labels routing calls")
	}
	if result["pr_number"] != 999 {
		t.Fatalf("expected PR number, got %#v", result["pr_number"])
	}
	if result["url"] != "https://github.com/acme/backend/pull/999" {
		t.Fatalf("expected PR URL, got %#v", result["url"])
	}
	if result["branch"] != "majdoor/revert-db-pool-config" {
		t.Fatalf("expected branch in result, got %#v", result["branch"])
	}
	if result["repository"] != "acme/backend" {
		t.Fatalf("expected repository in result, got %#v", result["repository"])
	}
	if result["base"] != "main" {
		t.Fatalf("expected base branch in result, got %#v", result["base"])
	}
	if result["head_sha"] != "head-sha-999" {
		t.Fatalf("expected head SHA in result, got %#v", result["head_sha"])
	}
	if result["draft"] != true {
		t.Fatalf("expected draft flag")
	}
	routing := result["routing"].(map[string]any)
	assertStringSlice(t, routing["reviewers"], []string{"octocat"})
	assertStringSlice(t, routing["team_reviewers"], []string{"platform"})
	assertStringSlice(t, routing["labels"], []string{"majdoor", "backend"})
}

func TestGitHubAdapterUpdatesPullRequest(t *testing.T) {
	var sawFileUpdate bool
	var sawComment bool
	pullReads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/backend/pulls/999":
			pullReads++
			w.Write([]byte(`{
				"number": 999,
				"title": "Draft: Revert backend database pool config",
				"state": "open",
				"html_url": "https://github.com/acme/backend/pull/999",
				"draft": true,
				"merged": false,
				"head": {"ref": "majdoor/revert-db-pool-config", "sha": "head-sha-999"},
				"base": {"ref": "main"}
			}`))
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/backend/contents/.github/workflows/ci.yml":
			if r.URL.Query().Get("ref") != "majdoor/revert-db-pool-config" {
				t.Fatalf("unexpected file ref: %s", r.URL.RawQuery)
			}
			w.Write([]byte(`{"sha":"file-sha-1","content":"","encoding":"base64"}`))
		case r.Method == http.MethodPut && r.URL.Path == "/repos/acme/backend/contents/.github/workflows/ci.yml":
			sawFileUpdate = true
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode payload: %v", err)
			}
			if payload["message"] != "Align CI Node.js version" {
				t.Fatalf("unexpected commit message: %#v", payload["message"])
			}
			if payload["branch"] != "majdoor/revert-db-pool-config" {
				t.Fatalf("unexpected branch: %#v", payload["branch"])
			}
			if payload["sha"] != "file-sha-1" {
				t.Fatalf("expected existing file sha")
			}
			content, err := base64.StdEncoding.DecodeString(payload["content"].(string))
			if err != nil {
				t.Fatalf("decode content: %v", err)
			}
			if string(content) != "node-version: 20\n" {
				t.Fatalf("unexpected content: %q", string(content))
			}
			w.Write([]byte(`{"content":{"sha":"file-sha-2"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/backend/issues/999/comments":
			sawComment = true
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode payload: %v", err)
			}
			if payload["body"] != "Majdoor follow-up fix" {
				t.Fatalf("unexpected comment: %#v", payload["body"])
			}
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"html_url":"https://github.com/acme/backend/pull/999#issuecomment-1"}`))
		default:
			t.Fatalf("unexpected GitHub request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	adapter := NewGitHubAdapter(GitHubAdapterConfig{
		Token:   "test-token",
		BaseURL: server.URL,
		Client:  server.Client(),
	})
	result, err := adapter.Execute(CapabilityDefinition{
		ID:       "code_host.update_pull_request",
		Provider: GitHubProvider,
	}, ToolCallRequest{
		Arguments: map[string]any{
			"repository":     "acme/backend",
			"pr_number":      999,
			"commit_message": "Align CI Node.js version",
			"comment":        "Majdoor follow-up fix",
			"files": map[string]any{
				".github/workflows/ci.yml": "node-version: 20\n",
			},
		},
	})
	if err != nil {
		t.Fatalf("expected PR update result, got error: %v", err)
	}
	if pullReads != 2 {
		t.Fatalf("expected initial and refreshed PR reads, got %d", pullReads)
	}
	if !sawFileUpdate {
		t.Fatalf("expected file update")
	}
	if !sawComment {
		t.Fatalf("expected PR comment")
	}
	if result["pr_number"] != 999 {
		t.Fatalf("expected PR number, got %#v", result["pr_number"])
	}
	if result["comment_url"] != "https://github.com/acme/backend/pull/999#issuecomment-1" {
		t.Fatalf("expected comment URL, got %#v", result["comment_url"])
	}
	if result["branch"] != "majdoor/revert-db-pool-config" {
		t.Fatalf("expected PR branch, got %#v", result["branch"])
	}
}

func TestGitHubAdapterGetsFile(t *testing.T) {
	var sawRef bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.EscapedPath() != "/repos/acme/backend/contents/config/database.yaml" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		if r.URL.Query().Get("ref") != "main" {
			t.Fatalf("unexpected ref: %q", r.URL.Query().Get("ref"))
		}
		sawRef = true
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"sha": "file-sha",
			"content": "bWF4X29wZW5fY29ubmVjdGlvbnM6IDUK",
			"encoding": "base64",
			"html_url": "https://github.com/acme/backend/blob/main/config/database.yaml"
		}`))
	}))
	defer server.Close()

	adapter := NewGitHubAdapter(GitHubAdapterConfig{
		Token:   "test-token",
		BaseURL: server.URL,
		Client:  server.Client(),
	})
	result, err := adapter.Execute(CapabilityDefinition{
		ID:       "code_host.get_file",
		Provider: GitHubProvider,
	}, ToolCallRequest{
		Arguments: map[string]any{
			"repository": "acme/backend",
			"path":       "config/database.yaml",
			"ref":        "main",
		},
	})
	if err != nil {
		t.Fatalf("expected file result, got error: %v", err)
	}
	if !sawRef {
		t.Fatalf("expected ref query")
	}
	if result["content"] != "max_open_connections: 5\n" {
		t.Fatalf("unexpected content: %#v", result["content"])
	}
	if result["path"] != "config/database.yaml" {
		t.Fatalf("unexpected path: %#v", result["path"])
	}
	if result["source_url"] != "https://github.com/acme/backend/blob/main/config/database.yaml" {
		t.Fatalf("unexpected source URL: %#v", result["source_url"])
	}
}

func TestGitHubAdapterGetsPullRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.EscapedPath() != "/repos/acme/backend/pulls/999" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"number": 999,
			"title": "Draft: Revert backend database pool config",
			"state": "closed",
			"html_url": "https://github.com/acme/backend/pull/999",
			"draft": false,
			"merged": true,
			"merged_at": "2026-07-13T07:00:00Z",
			"merge_commit_sha": "merge-sha-999",
			"head": {
				"ref": "majdoor/revert-db-pool-config",
				"sha": "head-sha-999"
			},
			"base": {
				"ref": "main"
			}
		}`))
	}))
	defer server.Close()

	adapter := NewGitHubAdapter(GitHubAdapterConfig{
		Token:   "test-token",
		BaseURL: server.URL,
		Client:  server.Client(),
	})
	result, err := adapter.Execute(CapabilityDefinition{
		ID:       "code_host.get_pull_request",
		Provider: GitHubProvider,
	}, ToolCallRequest{
		Arguments: map[string]any{
			"repository": "acme/backend",
			"pr_number":  999,
		},
	})
	if err != nil {
		t.Fatalf("expected pull request result, got error: %v", err)
	}
	if result["merged"] != true {
		t.Fatalf("expected merged PR, got %#v", result["merged"])
	}
	if result["merge_commit_sha"] != "merge-sha-999" {
		t.Fatalf("expected merge commit sha, got %#v", result["merge_commit_sha"])
	}
	if result["base"] != "main" {
		t.Fatalf("expected base branch, got %#v", result["base"])
	}
}

func TestGitHubAdapterMarksDraftPRReadyForReview(t *testing.T) {
	var pullReads int
	var sawGraphQL bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/repos/acme/backend/pulls/999" {
			pullReads++
			draft := "true"
			if pullReads > 1 {
				draft = "false"
			}
			w.Write([]byte(`{
				"number": 999,
				"node_id": "PR_kwDOExample",
				"title": "Draft: Revert backend database pool config",
				"state": "open",
				"html_url": "https://github.com/acme/backend/pull/999",
				"draft": ` + draft + `,
				"merged": false,
				"head": {
					"ref": "majdoor/revert-db-pool-config",
					"sha": "head-sha-999"
				},
				"base": {
					"ref": "main"
				}
			}`))
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/graphql" {
			sawGraphQL = true
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode graphQL payload: %v", err)
			}
			variables, ok := payload["variables"].(map[string]any)
			if !ok {
				t.Fatalf("expected variables object")
			}
			if variables["pullRequestId"] != "PR_kwDOExample" {
				t.Fatalf("unexpected pullRequestId: %#v", variables["pullRequestId"])
			}
			w.Write([]byte(`{"data":{"markPullRequestReadyForReview":{"pullRequest":{"number":999,"isDraft":false,"url":"https://github.com/acme/backend/pull/999"}}}}`))
			return
		}
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	adapter := NewGitHubAdapter(GitHubAdapterConfig{
		Token:   "test-token",
		BaseURL: server.URL,
		Client:  server.Client(),
	})
	result, err := adapter.Execute(CapabilityDefinition{
		ID:       "code_host.mark_ready_for_review",
		Provider: GitHubProvider,
	}, ToolCallRequest{
		Arguments: map[string]any{
			"repository": "acme/backend",
			"pr_number":  999,
		},
	})
	if err != nil {
		t.Fatalf("expected ready-for-review result, got error: %v", err)
	}
	if !sawGraphQL {
		t.Fatalf("expected GraphQL ready-for-review mutation")
	}
	if pullReads != 2 {
		t.Fatalf("expected initial and refreshed PR reads, got %d", pullReads)
	}
	if result["ready_for_review"] != true {
		t.Fatalf("expected ready_for_review true, got %#v", result["ready_for_review"])
	}
	if result["draft"] != false {
		t.Fatalf("expected draft false, got %#v", result["draft"])
	}
	if result["base"] != "main" {
		t.Fatalf("expected base branch, got %#v", result["base"])
	}
	if result["head_sha"] != "head-sha-999" {
		t.Fatalf("expected head SHA, got %#v", result["head_sha"])
	}
}

func TestGitHubAdapterCreatesBranchFilesAndDraftPR(t *testing.T) {
	var createdBranch bool
	var wroteFile bool
	var createdPR bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		requestPath := r.URL.EscapedPath()
		switch {
		case r.Method == http.MethodGet && requestPath == "/repos/acme/backend/git/ref/heads/majdoor%2Fconfig":
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"message":"not found"}`))
		case r.Method == http.MethodGet && requestPath == "/repos/acme/backend/git/ref/heads/main":
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"object":{"sha":"base-sha"}}`))
		case r.Method == http.MethodPost && requestPath == "/repos/acme/backend/git/refs":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode branch payload: %v", err)
			}
			if payload["ref"] != "refs/heads/majdoor/config" {
				t.Fatalf("unexpected branch ref: %#v", payload["ref"])
			}
			if payload["sha"] != "base-sha" {
				t.Fatalf("unexpected branch sha: %#v", payload["sha"])
			}
			createdBranch = true
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"object":{"sha":"base-sha"}}`))
		case r.Method == http.MethodGet && requestPath == "/repos/acme/backend/contents/config/database.yaml":
			if r.URL.Query().Get("ref") != "majdoor/config" {
				t.Fatalf("unexpected content ref: %q", r.URL.Query().Get("ref"))
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"sha":"existing-file-sha"}`))
		case r.Method == http.MethodPut && requestPath == "/repos/acme/backend/contents/config/database.yaml":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode file payload: %v", err)
			}
			if payload["message"] != "Update database pool config" {
				t.Fatalf("unexpected commit message: %#v", payload["message"])
			}
			if payload["branch"] != "majdoor/config" {
				t.Fatalf("unexpected file branch: %#v", payload["branch"])
			}
			if payload["sha"] != "existing-file-sha" {
				t.Fatalf("unexpected file sha: %#v", payload["sha"])
			}
			decoded, err := base64.StdEncoding.DecodeString(payload["content"].(string))
			if err != nil {
				t.Fatalf("decode content: %v", err)
			}
			if string(decoded) != "max_open_connections: 50\n" {
				t.Fatalf("unexpected file content: %q", string(decoded))
			}
			wroteFile = true
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"content":{"sha":"new-file-sha"}}`))
		case r.Method == http.MethodPost && requestPath == "/repos/acme/backend/pulls":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode pull payload: %v", err)
			}
			if payload["head"] != "majdoor/config" {
				t.Fatalf("unexpected pull head: %#v", payload["head"])
			}
			createdPR = true
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{
				"number": 1000,
				"title": "Draft: Update database pool config",
				"html_url": "https://github.com/acme/backend/pull/1000",
				"draft": true,
				"head": {
					"ref": "majdoor/config",
					"sha": "head-sha-1000"
				}
			}`))
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	adapter := NewGitHubAdapter(GitHubAdapterConfig{
		Token:   "test-token",
		BaseURL: server.URL,
		Client:  server.Client(),
	})
	result, err := adapter.Execute(CapabilityDefinition{
		ID:       "code_host.create_draft_pr",
		Provider: GitHubProvider,
	}, ToolCallRequest{
		Arguments: map[string]any{
			"repository":     "acme/backend",
			"title":          "Draft: Update database pool config",
			"head":           "majdoor/config",
			"base":           "main",
			"commit_message": "Update database pool config",
			"files": map[string]any{
				"config/database.yaml": "max_open_connections: 50\n",
			},
		},
	})
	if err != nil {
		t.Fatalf("expected branch, file, and PR result, got error: %v", err)
	}
	if !createdBranch || !wroteFile || !createdPR {
		t.Fatalf("expected branch/file/pr operations, got branch=%v file=%v pr=%v", createdBranch, wroteFile, createdPR)
	}
	if result["pr_number"] != 1000 {
		t.Fatalf("expected PR number, got %#v", result["pr_number"])
	}
	if result["head_sha"] != "head-sha-1000" {
		t.Fatalf("expected head SHA, got %#v", result["head_sha"])
	}
}

func TestGitHubAdapterGetsRecentChanges(t *testing.T) {
	var sawAuth bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/backend/pulls" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("state") != "closed" {
			t.Fatalf("expected closed pull requests query")
		}
		if r.URL.Query().Get("per_page") != "4" {
			t.Fatalf("expected doubled per_page for filtering, got %q", r.URL.Query().Get("per_page"))
		}
		if r.URL.Query().Get("base") != "main" {
			t.Fatalf("expected base branch")
		}
		if r.Header.Get("Authorization") == "Bearer test-token" {
			sawAuth = true
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[
			{
				"number": 10,
				"title": "Tune database pool defaults",
				"html_url": "https://github.com/acme/backend/pull/10",
				"merged_at": "2026-07-09T09:38:00Z",
				"updated_at": "2026-07-09T09:39:00Z",
				"changed_files": 2,
				"user": {"login": "octocat"}
			},
			{
				"number": 9,
				"title": "Closed but not merged",
				"html_url": "https://github.com/acme/backend/pull/9",
				"merged_at": null,
				"updated_at": "2026-07-09T09:00:00Z",
				"changed_files": 1,
				"user": {"login": "octocat"}
			},
			{
				"number": 8,
				"title": "Update retry timeout",
				"html_url": "https://github.com/acme/backend/pull/8",
				"merged_at": "2026-07-09T08:00:00Z",
				"updated_at": "2026-07-09T08:01:00Z",
				"changed_files": 4,
				"user": {"login": "hubot"}
			}
		]`))
	}))
	defer server.Close()

	adapter := NewGitHubAdapter(GitHubAdapterConfig{
		Token:   "test-token",
		BaseURL: server.URL,
		Client:  server.Client(),
	})
	result, err := adapter.Execute(CapabilityDefinition{
		ID:       "code_host.get_recent_changes",
		Provider: GitHubProvider,
	}, ToolCallRequest{
		Arguments: map[string]any{
			"repository": "acme/backend",
			"branch":     "main",
			"limit":      2,
		},
	})
	if err != nil {
		t.Fatalf("expected recent changes result, got error: %v", err)
	}
	if !sawAuth {
		t.Fatalf("expected authorization header")
	}
	changes, ok := result["changes"].([]map[string]any)
	if !ok {
		t.Fatalf("expected normalized changes")
	}
	if len(changes) != 2 {
		t.Fatalf("expected two merged changes, got %d", len(changes))
	}
	if changes[0]["pr"] != 10 {
		t.Fatalf("expected first PR #10, got %#v", changes[0]["pr"])
	}
	if changes[1]["pr"] != 8 {
		t.Fatalf("expected second merged PR #8, got %#v", changes[1]["pr"])
	}
	if result["evidence"] != "GitHub returned 2 merged pull request change(s) for acme/backend." {
		t.Fatalf("unexpected evidence: %#v", result["evidence"])
	}
}

func TestGitHubAdapterGetsChecksForCommitRef(t *testing.T) {
	var sawAuth bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/backend/commits/sha123/check-runs" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") == "Bearer test-token" {
			sawAuth = true
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"total_count": 2,
			"check_runs": [
				{"name": "unit-tests", "status": "completed", "conclusion": "success", "html_url": "https://github.com/acme/backend/runs/1"},
				{"name": "lint", "status": "completed", "conclusion": "success", "html_url": "https://github.com/acme/backend/runs/2"}
			]
		}`))
	}))
	defer server.Close()

	adapter := NewGitHubAdapter(GitHubAdapterConfig{
		Token:   "test-token",
		BaseURL: server.URL,
		Client:  server.Client(),
	})
	result, err := adapter.Execute(CapabilityDefinition{
		ID:       "ci.get_checks",
		Provider: GitHubProvider,
	}, ToolCallRequest{
		Arguments: map[string]any{
			"repository": "acme/backend",
			"commit_sha": "sha123",
		},
	})
	if err != nil {
		t.Fatalf("expected checks result, got error: %v", err)
	}
	if !sawAuth {
		t.Fatalf("expected authorization header")
	}
	if result["status"] != "passed" {
		t.Fatalf("expected passed status, got %#v", result["status"])
	}
	if result["commit_sha"] != "sha123" {
		t.Fatalf("expected commit SHA in result")
	}
	checks, ok := result["checks"].([]map[string]any)
	if !ok {
		t.Fatalf("expected normalized checks")
	}
	if len(checks) != 2 {
		t.Fatalf("expected two checks, got %d", len(checks))
	}
}

func TestGitHubAdapterGetsChecksForPullRequestNumber(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/acme/backend/pulls/42":
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"head": {"sha": "head-sha-42"}}`))
		case "/repos/acme/backend/commits/head-sha-42/check-runs":
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{
				"total_count": 1,
				"check_runs": [
					{"name": "unit-tests", "status": "completed", "conclusion": "failure", "html_url": "https://github.com/acme/backend/runs/3"}
				]
			}`))
		case "/repos/acme/backend/actions/runs":
			if r.URL.Query().Get("head_sha") != "head-sha-42" {
				t.Fatalf("expected head_sha query, got %q", r.URL.Query().Get("head_sha"))
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{
				"total_count": 1,
				"workflow_runs": [{
					"id": 9001,
					"name": "CI",
					"status": "completed",
					"conclusion": "failure",
					"html_url": "https://github.com/acme/backend/actions/runs/9001",
					"head_sha": "head-sha-42"
				}]
			}`))
		case "/repos/acme/backend/actions/runs/9001/jobs":
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{
				"total_count": 1,
				"jobs": [{
					"id": 7001,
					"name": "unit-tests",
					"status": "completed",
					"conclusion": "failure",
					"html_url": "https://github.com/acme/backend/actions/runs/9001/job/7001",
					"logs_url": "https://api.github.test/repos/acme/backend/actions/jobs/7001/logs"
				}]
			}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	adapter := NewGitHubAdapter(GitHubAdapterConfig{
		Token:   "test-token",
		BaseURL: server.URL,
		Client:  server.Client(),
	})
	result, err := adapter.Execute(CapabilityDefinition{
		ID:       "ci.get_checks",
		Provider: GitHubProvider,
	}, ToolCallRequest{
		Arguments: map[string]any{
			"owner":     "acme",
			"repo":      "backend",
			"pr_number": 42,
		},
	})
	if err != nil {
		t.Fatalf("expected PR checks result, got error: %v", err)
	}
	if result["status"] != "failed" {
		t.Fatalf("expected failed status, got %#v", result["status"])
	}
	if result["commit_sha"] != "head-sha-42" {
		t.Fatalf("expected PR head SHA, got %#v", result["commit_sha"])
	}
	if result["job_id"] != int64(7001) {
		t.Fatalf("expected failed job id, got %#v", result["job_id"])
	}
	if result["logs_url"] != "https://api.github.test/repos/acme/backend/actions/jobs/7001/logs" {
		t.Fatalf("expected logs URL, got %#v", result["logs_url"])
	}
	checks, ok := result["checks"].([]map[string]any)
	if !ok || checks[0]["job_id"] != int64(7001) {
		t.Fatalf("expected failed check to include job metadata, got %#v", result["checks"])
	}
}

func TestGitHubAdapterGetsLogsFromLogsURL(t *testing.T) {
	var sawAuth bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/logs/job-1" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") == "Bearer test-token" {
			sawAuth = true
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("unit-tests\nerror: assertion failed\n"))
	}))
	defer server.Close()

	adapter := NewGitHubAdapter(GitHubAdapterConfig{
		Token:   "test-token",
		BaseURL: server.URL,
		Client:  server.Client(),
	})
	result, err := adapter.Execute(CapabilityDefinition{
		ID:       "ci.get_logs",
		Provider: GitHubProvider,
	}, ToolCallRequest{
		Arguments: map[string]any{
			"logs_url": server.URL + "/logs/job-1",
		},
	})
	if err != nil {
		t.Fatalf("expected logs result, got error: %v", err)
	}
	if !sawAuth {
		t.Fatalf("expected authorization header")
	}
	if result["summary"] != "GitHub CI logs contain failure indicators." {
		t.Fatalf("expected failure summary, got %#v", result["summary"])
	}
	if result["log_excerpt"] != "unit-tests\nerror: assertion failed\n" {
		t.Fatalf("expected log excerpt, got %#v", result["log_excerpt"])
	}
	if result["truncated"] != false {
		t.Fatalf("expected logs to fit without truncation")
	}
}

func TestGitHubAdapterGetsLogsFromJobID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/backend/actions/jobs/123/logs" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("build\nok\n"))
	}))
	defer server.Close()

	adapter := NewGitHubAdapter(GitHubAdapterConfig{
		Token:   "test-token",
		BaseURL: server.URL,
		Client:  server.Client(),
	})
	result, err := adapter.Execute(CapabilityDefinition{
		ID:       "ci.get_logs",
		Provider: GitHubProvider,
	}, ToolCallRequest{
		Arguments: map[string]any{
			"repository": "acme/backend",
			"job_id":     123,
		},
	})
	if err != nil {
		t.Fatalf("expected job logs result, got error: %v", err)
	}
	if result["summary"] != "GitHub CI logs fetched without obvious failure indicators." {
		t.Fatalf("expected clean summary, got %#v", result["summary"])
	}
	if result["source_url"] != server.URL+"/repos/acme/backend/actions/jobs/123/logs" {
		t.Fatalf("expected source URL, got %#v", result["source_url"])
	}
}

func TestGitHubAdapterGetsRecentDeploysFromWorkflowRuns(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.EscapedPath() != "/repos/acme/backend/actions/workflows/deploy-backend.yml/runs" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		if r.URL.Query().Get("branch") != "main" {
			t.Fatalf("expected branch query, got %q", r.URL.Query().Get("branch"))
		}
		if r.URL.Query().Get("head_sha") != "merge-sha-999" {
			t.Fatalf("expected head_sha query, got %q", r.URL.Query().Get("head_sha"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"total_count": 1,
			"workflow_runs": [{
				"id": 1001,
				"name": "Deploy backend",
				"status": "completed",
				"conclusion": "success",
				"html_url": "https://github.com/acme/backend/actions/runs/1001",
				"head_sha": "merge-sha-999",
				"head_branch": "main",
				"event": "push",
				"created_at": "2026-07-13T07:01:00Z",
				"updated_at": "2026-07-13T07:04:00Z",
				"run_started_at": "2026-07-13T07:01:30Z",
				"workflow_id": 42
			}]
		}`))
	}))
	defer server.Close()

	adapter := NewGitHubAdapter(GitHubAdapterConfig{
		Token:   "test-token",
		BaseURL: server.URL,
		Client:  server.Client(),
	})
	result, err := adapter.Execute(CapabilityDefinition{
		ID:       "deploy.get_recent_deploys",
		Provider: GitHubProvider,
	}, ToolCallRequest{
		Arguments: map[string]any{
			"repository": "acme/backend",
			"workflow":   "deploy-backend.yml",
			"branch":     "main",
			"commit_sha": "merge-sha-999",
		},
	})
	if err != nil {
		t.Fatalf("expected deploy result, got error: %v", err)
	}
	if result["status"] != "succeeded" {
		t.Fatalf("expected succeeded deploy status, got %#v", result["status"])
	}
	deploys, ok := result["deploys"].([]map[string]any)
	if !ok || len(deploys) != 1 {
		t.Fatalf("expected one deploy, got %#v", result["deploys"])
	}
	if deploys[0]["commit_sha"] != "merge-sha-999" || deploys[0]["branch"] != "main" {
		t.Fatalf("unexpected deploy metadata: %#v", deploys[0])
	}
}

func TestGitHubProviderOverridesCodeAndCICapabilities(t *testing.T) {
	registry := DefaultCapabilityRegistry().WithProviderOverrides(GitHubProviderOverrides())
	for _, id := range []string{
		"code_host.get_recent_changes",
		"code_host.get_file",
		"code_host.get_pull_request",
		"code_host.create_draft_pr",
		"code_host.update_pull_request",
		"code_host.mark_ready_for_review",
		"ci.get_checks",
		"ci.get_logs",
	} {
		definition, ok := registry.byID[id]
		if !ok {
			t.Fatalf("expected capability %q", id)
		}
		if definition.Provider != GitHubProvider {
			t.Fatalf("expected %q provider for %q, got %q", GitHubProvider, id, definition.Provider)
		}
	}

	definition, ok := registry.byID["metrics.get_service_health"]
	if !ok {
		t.Fatalf("expected metrics capability")
	}
	if definition.Provider != "mock" {
		t.Fatalf("expected metrics provider to remain mock, got %q", definition.Provider)
	}
}

func TestGitHubDeployProviderOverridesDeploymentCapability(t *testing.T) {
	registry := DefaultCapabilityRegistry().WithProviderOverrides(GitHubDeployProviderOverrides())
	deploy, ok := registry.byID["deploy.get_recent_deploys"]
	if !ok {
		t.Fatalf("expected deploy capability")
	}
	if deploy.Provider != GitHubProvider {
		t.Fatalf("expected github deploy provider, got %q", deploy.Provider)
	}
	codeHost, ok := registry.byID["code_host.create_draft_pr"]
	if !ok {
		t.Fatalf("expected code host capability")
	}
	if codeHost.Provider != "mock" {
		t.Fatalf("expected code host provider to remain mock, got %q", codeHost.Provider)
	}
}

func TestSentryProviderOverridesErrorsCapability(t *testing.T) {
	registry := DefaultCapabilityRegistry().WithProviderOverrides(SentryProviderOverrides())
	errorsCapability, ok := registry.byID["errors.get_recent_errors"]
	if !ok {
		t.Fatalf("expected errors capability")
	}
	if errorsCapability.Provider != SentryProvider {
		t.Fatalf("expected sentry provider, got %q", errorsCapability.Provider)
	}
	metrics, ok := registry.byID["metrics.get_service_health"]
	if !ok {
		t.Fatalf("expected metrics capability")
	}
	if metrics.Provider != "mock" {
		t.Fatalf("expected metrics provider to remain mock, got %q", metrics.Provider)
	}
}

func TestPrometheusProviderOverridesMetricsCapability(t *testing.T) {
	registry := DefaultCapabilityRegistry().WithProviderOverrides(PrometheusProviderOverrides())
	metrics, ok := registry.byID["metrics.get_service_health"]
	if !ok {
		t.Fatalf("expected metrics capability")
	}
	if metrics.Provider != PrometheusProvider {
		t.Fatalf("expected prometheus provider, got %q", metrics.Provider)
	}
	errorsCapability, ok := registry.byID["errors.get_recent_errors"]
	if !ok {
		t.Fatalf("expected errors capability")
	}
	if errorsCapability.Provider != "mock" {
		t.Fatalf("expected errors provider to remain mock, got %q", errorsCapability.Provider)
	}
}

func TestKubernetesProviderOverridesRuntimeCapability(t *testing.T) {
	registry := DefaultCapabilityRegistry().WithProviderOverrides(KubernetesProviderOverrides())
	runtimeCapability, ok := registry.byID["runtime.get_workload_status"]
	if !ok {
		t.Fatalf("expected runtime capability")
	}
	if runtimeCapability.Provider != KubernetesProvider {
		t.Fatalf("expected kubernetes provider, got %q", runtimeCapability.Provider)
	}
	metrics, ok := registry.byID["metrics.get_service_health"]
	if !ok {
		t.Fatalf("expected metrics capability")
	}
	if metrics.Provider != "mock" {
		t.Fatalf("expected metrics provider to remain mock, got %q", metrics.Provider)
	}
}

func assertStringSlice(t *testing.T, value any, expected []string) {
	t.Helper()
	items, ok := value.([]any)
	if !ok {
		if typed, ok := value.([]string); ok {
			if len(typed) != len(expected) {
				t.Fatalf("expected %#v, got %#v", expected, typed)
			}
			for index := range expected {
				if typed[index] != expected[index] {
					t.Fatalf("expected %#v, got %#v", expected, typed)
				}
			}
			return
		}
		t.Fatalf("expected string slice, got %#v", value)
	}
	if len(items) != len(expected) {
		t.Fatalf("expected %#v, got %#v", expected, items)
	}
	for index := range expected {
		if items[index] != expected[index] {
			t.Fatalf("expected %#v, got %#v", expected, items)
		}
	}
}

func testGitHubAppPrivateKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}))
}

func assertJWTIssuer(t *testing.T, token string, expectedIssuer string) {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("expected JWT with three parts")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode jwt payload: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatalf("decode jwt claims: %v", err)
	}
	if claims["iss"] != expectedIssuer {
		t.Fatalf("expected issuer %q, got %#v", expectedIssuer, claims["iss"])
	}
}
