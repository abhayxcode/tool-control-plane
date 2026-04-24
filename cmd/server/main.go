package main

import (
	"encoding/json"
	"log"
	"net/http"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /v1/capabilities", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string][]string{
			"capabilities": {
				"metrics.get_service_health",
				"errors.get_recent_errors",
				"deploy.get_recent_deploys",
				"code_host.get_recent_changes",
				"runtime.get_workload_status",
				"docs.search_runbooks",
			},
		})
	})

	log.Println("tool-control-plane listening on :4100")
	log.Fatal(http.ListenAndServe(":4100", mux))
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
