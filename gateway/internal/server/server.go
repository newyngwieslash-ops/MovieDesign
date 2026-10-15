package server

import (
	"database/sql"
	"encoding/json"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/delivery"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/workspace"
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

type Server struct {
	http.Handler
	workspace *workspace.Workspace
}

func (s *Server) Close() error { return s.workspace.Close() }
func New(origin string, db *sql.DB, workspaceRoot string) (*Server, error) {
	ws, err := workspace.Open(workspaceRoot)
	if err != nil {
		return nil, err
	}
	srv := &Server{workspace: ws}
	secrets, err := providers.OpenSecrets(filepath.Join(filepath.Dir(workspaceRoot), "credentials"))
	if err != nil {
		ws.Close()
		return nil, err
	}
	srv.Handler = newHandler(origin, db, workspace.NewService(db, ws), providers.NewService(db, secrets))
	return srv, nil
}
func newHandler(origin string, db *sql.DB, files *workspace.Service, configured ...*providers.Service) http.Handler {
	var providerService *providers.Service
	if len(configured) > 0 {
		providerService = configured[0]
	}
	var deliveries *delivery.Service
	if files != nil {
		deliveries = delivery.NewService(db, files.Workspace, "user")
	}
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
		kind := objectKind(r.URL.Path)
		if r.Method == http.MethodOptions {
			method := r.Header.Get("Access-Control-Request-Method")
			allowed := (r.URL.Path == "/api/health" && method == "GET") || (r.URL.Path == "/api/providers" && (method == "GET" || method == "POST")) || (r.URL.Path == "/api/providers/receipt" && method == "GET") || (r.URL.Path == "/api/deliver" && method == "POST") || (kind != "" && (method == "GET" || method == "POST")) || (fileRoute(r.URL.Path) && fileMethod(r.URL.Path, method)) || (canvasRoute(r.URL.Path) && canvasMethod(r.URL.Path, method))
			if !allowed || !allowedPreflightHeaders(r.Header.Values("Access-Control-Request-Headers")) {
				reject(w, http.StatusForbidden, "invalid_input", "Preflight is not allowed")
				return
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Last-Event-ID, Range")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path == "/api/providers" || r.URL.Path == "/api/providers/receipt" {
			serveProviders(w, r, providerService)
			return
		}
		if canvasRoute(r.URL.Path) {
			serveCanvas(w, r, store.Canvas{DB: db})
			return
		}
		if r.URL.Path == "/api/deliver" {
			serveDelivery(w, r, deliveries)
			return
		}
		if fileRoute(r.URL.Path) {
			serveFiles(w, r, files)
			return
		}
		if kind != "" {
			serveObjects(w, r, store.Objects{DB: db}, kind, files)
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

func allowedPreflightHeaders(values []string) bool {
	for _, value := range values {
		for _, header := range strings.Split(value, ",") {
			switch strings.ToLower(strings.TrimSpace(header)) {
			case "", "content-type", "last-event-id", "range":
			default:
				return false
			}
		}
	}
	return true
}
