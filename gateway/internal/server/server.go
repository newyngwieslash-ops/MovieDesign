package server

import (
	"encoding/json"
	"net/http"
)

type Health struct {
	Status        string `json:"status"`
	Idle          bool   `json:"idle"`
	SafeToSuspend bool   `json:"safe_to_suspend"`
}

func Ready() Health { return Health{Status: "ok", Idle: true, SafeToSuspend: true} }

func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func reject(w http.ResponseWriter, status int, code, message string) {
	respond(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func Handler(origin string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Vary", "Origin")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		// CORS headers alone do not prevent CSRF: reject before dispatching any route.
		origins := r.Header.Values("Origin")
		if len(origins) != 1 || origins[0] != origin || origin == "" || origin == "null" {
			reject(w, http.StatusForbidden, "invalid_input", "Origin is not allowed")
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		if r.Method == http.MethodOptions {
			if r.URL.Path != "/api/health" || r.Header.Get("Access-Control-Request-Method") != "GET" || r.Header.Get("Access-Control-Request-Headers") != "" {
				reject(w, http.StatusForbidden, "invalid_input", "Preflight is not allowed")
				return
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path != "/api/health" {
			reject(w, 404, "not_found", "Route not found")
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			reject(w, 405, "invalid_input", "Method not allowed")
			return
		}
		respond(w, http.StatusOK, Ready())
	})
}
