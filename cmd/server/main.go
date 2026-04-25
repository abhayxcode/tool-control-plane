package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"

	"github.com/abhayxcode/tool-control-plane/api"
	"github.com/abhayxcode/tool-control-plane/internal/controlplane"
)

func main() {
	svc := newServiceFromEnv()
	mux := newMux(svc)

	log.Println("tool-control-plane listening on :4100")
	log.Fatal(http.ListenAndServe(":4100", mux))
}

func newServiceFromEnv() *controlplane.Service {
	registry := controlplane.DefaultCapabilityRegistry()
	adapters := controlplane.DefaultAdapterRegistry()
	store := controlplane.Store(controlplane.NewMemoryStore())
	if os.Getenv("TOOL_CONTROL_PLANE_CODE_PROVIDER") == controlplane.GitHubProvider {
		registry = registry.WithProviderOverrides(controlplane.GitHubProviderOverrides())
		adapters = controlplane.DefaultAdapterRegistryWithGitHub(controlplane.GitHubAdapterConfig{
			Token:   os.Getenv("GITHUB_TOKEN"),
			BaseURL: os.Getenv("GITHUB_API_BASE_URL"),
		})
	}
	if os.Getenv("TOOL_CONTROL_PLANE_STORE") == "sqlite" || os.Getenv("TOOL_CONTROL_PLANE_SQLITE_PATH") != "" {
		path := os.Getenv("TOOL_CONTROL_PLANE_SQLITE_PATH")
		if path == "" {
			path = "tool-control-plane.sqlite3"
		}
		sqliteStore, err := controlplane.NewSQLiteStore(path)
		if err != nil {
			log.Fatalf("open sqlite store: %v", err)
		}
		store = sqliteStore
	}
	return controlplane.NewServiceWithOptions(controlplane.ServiceOptions{
		Registry: registry,
		Adapters: adapters,
		Store:    store,
	})
}

func newMux(svc *controlplane.Service) *http.ServeMux {
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
			"capabilities": svc.Capabilities(),
			"details":      svc.CapabilityDetails(),
		})
	})
	mux.HandleFunc("POST /v1/tool-calls", func(w http.ResponseWriter, r *http.Request) {
		var req controlplane.ToolCallRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
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

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func decodeApprovalDecision(w http.ResponseWriter, r *http.Request) (controlplane.ApprovalDecisionRequest, bool) {
	var req controlplane.ApprovalDecisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return req, false
	}
	return req, true
}
