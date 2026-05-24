package main

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/abhayxcode/tool-control-plane/api"
	"github.com/abhayxcode/tool-control-plane/internal/controlplane"
)

type requestIDContextKey struct{}

var requestSeq uint64

func main() {
	config, err := configFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	svc, err := newServiceFromConfig(config)
	if err != nil {
		log.Fatal(err)
	}
	handler := newHandler(config, svc)
	server := newHTTPServer(config, handler)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Printf("tool-control-plane listening on %s", config.Addr)
	if err := runHTTPServer(ctx, server, config.ShutdownTimeout); err != nil {
		log.Fatal(err)
	}
}

func newMux(svc *controlplane.Service, configs ...Config) *http.ServeMux {
	config := Config{}
	if len(configs) > 0 {
		config = configs[0]
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write(api.OpenAPISpec)
	})
	mux.HandleFunc("GET /v1/capabilities", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"capabilities":    svc.Capabilities(),
			"details":         svc.CapabilityDetails(),
			"provider_config": providerConfigSummary(config),
		})
	})
	mux.HandleFunc("POST /v1/tool-calls", func(w http.ResponseWriter, r *http.Request) {
		var req controlplane.ToolCallRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.RequestID == "" {
			req.RequestID = requestIDFromContext(r.Context())
		}
		writeJSON(w, svc.CallTool(req))
	})
	mux.HandleFunc("GET /v1/audit", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"entries": svc.Audit()})
	})
	mux.HandleFunc("GET /v1/approvals", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"approvals": svc.Approvals()})
	})
	mux.HandleFunc("GET /v1/approvals/{id}", func(w http.ResponseWriter, r *http.Request) {
		approval, ok := svc.Approval(r.PathValue("id"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, approval)
	})
	mux.HandleFunc("POST /v1/approvals/{id}/grant", func(w http.ResponseWriter, r *http.Request) {
		req, ok := decodeApprovalDecision(w, r)
		if !ok {
			return
		}
		result, found := svc.GrantApproval(r.PathValue("id"), req)
		if !found {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, result)
	})
	mux.HandleFunc("POST /v1/approvals/{id}/deny", func(w http.ResponseWriter, r *http.Request) {
		req, ok := decodeApprovalDecision(w, r)
		if !ok {
			return
		}
		result, found := svc.DenyApproval(r.PathValue("id"), req)
		if !found {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, result)
	})
	mux.HandleFunc("POST /v1/approvals/{id}/execute", func(w http.ResponseWriter, r *http.Request) {
		result, found := svc.ExecuteApproval(r.PathValue("id"))
		if !found {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, result)
	})
	return mux
}

func providerConfigSummary(config Config) map[string]any {
	codeProvider := providerOrMock(config.CodeProvider)
	deployProvider := providerOrMock(config.DeployProvider)
	githubSelected := codeProvider == controlplane.GitHubProvider || deployProvider == controlplane.GitHubProvider
	githubConfigured := strings.TrimSpace(config.GitHubToken) != ""
	warnings := []string{}
	if githubSelected && !githubConfigured {
		warnings = append(warnings, "GITHUB_TOKEN is required when a GitHub provider is selected.")
	}
	return map[string]any{
		"code_provider":           codeProvider,
		"deploy_provider":         deployProvider,
		"github_selected":         githubSelected,
		"github_token_configured": githubConfigured,
		"github_base_url_set":     strings.TrimSpace(config.GitHubBaseURL) != "",
		"store":                   providerStore(config),
		"ready":                   !githubSelected || githubConfigured,
		"warnings":                warnings,
	}
}

func providerOrMock(provider string) string {
	if strings.TrimSpace(provider) == "" {
		return "mock"
	}
	return provider
}

func providerStore(config Config) string {
	if strings.TrimSpace(config.Store) != "" {
		return config.Store
	}
	if strings.TrimSpace(config.SQLitePath) != "" {
		return "sqlite"
	}
	return "memory"
}

func withBearerAuth(next http.Handler, token string) http.Handler {
	token = strings.TrimSpace(token)
	if token == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type rateLimiter struct {
	mu        sync.Mutex
	limit     int
	window    time.Duration
	now       func() time.Time
	instances map[string]rateLimitInstance
}

type rateLimitInstance struct {
	windowStart time.Time
	count       int
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	if window <= 0 {
		window = time.Minute
	}
	return &rateLimiter{
		limit:     limit,
		window:    window,
		now:       time.Now,
		instances: map[string]rateLimitInstance{},
	}
}

func (l *rateLimiter) allow(key string) bool {
	if l == nil || l.limit <= 0 {
		return true
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	instance := l.instances[key]
	if instance.windowStart.IsZero() || now.Sub(instance.windowStart) >= l.window {
		instance = rateLimitInstance{windowStart: now}
	}
	if instance.count >= l.limit {
		l.instances[key] = instance
		return false
	}
	instance.count++
	l.instances[key] = instance
	return true
}

func withRateLimit(next http.Handler, limiter *rateLimiter) http.Handler {
	if limiter == nil || limiter.limit <= 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		if !limiter.allow(rateLimitKey(r)) {
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func rateLimitKey(r *http.Request) string {
	if authorization := strings.TrimSpace(r.Header.Get("Authorization")); authorization != "" {
		return authorization
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	return r.RemoteAddr
}

func withRequestLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := requestID(r)
		recorder := &statusRecorder{
			ResponseWriter: w,
			status:         http.StatusOK,
		}
		recorder.Header().Set("X-Request-ID", requestID)
		startedAt := time.Now()
		ctx := context.WithValue(r.Context(), requestIDContextKey{}, requestID)
		next.ServeHTTP(recorder, r.WithContext(ctx))
		logAccess(r, requestID, recorder.status, time.Since(startedAt))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func requestID(r *http.Request) string {
	if value := strings.TrimSpace(r.Header.Get("X-Request-ID")); value != "" {
		return value
	}
	seq := atomic.AddUint64(&requestSeq, 1)
	return "req_" + time.Now().UTC().Format("20060102150405") + "_" + strconv.FormatUint(seq, 10)
}

func requestIDFromContext(ctx context.Context) string {
	value, _ := ctx.Value(requestIDContextKey{}).(string)
	return value
}

func logAccess(r *http.Request, requestID string, status int, duration time.Duration) {
	payload := map[string]any{
		"event":       "http_request",
		"request_id":  requestID,
		"method":      r.Method,
		"path":        r.URL.Path,
		"status":      status,
		"duration_ms": duration.Milliseconds(),
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		log.Printf("http_request request_id=%s method=%s path=%s status=%d duration_ms=%d", requestID, r.Method, r.URL.Path, status, duration.Milliseconds())
		return
	}
	log.Print(string(encoded))
}

func decodeApprovalDecision(w http.ResponseWriter, r *http.Request) (controlplane.ApprovalDecisionRequest, bool) {
	var req controlplane.ApprovalDecisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return req, false
	}
	return req, true
}
