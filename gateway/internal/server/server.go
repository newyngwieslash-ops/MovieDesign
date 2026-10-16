package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/delivery"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/events"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/tasks"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

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
	db        *sql.DB
	tasks     *tasks.Service
	events    *events.Service
	mu        sync.Mutex
	stopping  bool
	requests  sync.WaitGroup
	active    atomic.Int64
}

func (s *Server) BeginShutdown() {
	s.mu.Lock()
	s.stopping = true
	s.mu.Unlock()
	s.tasks.Stop()
	s.events.Close()
}
func (s *Server) Close() error {
	s.BeginShutdown()
	s.requests.Wait()
	s.tasks.Close()
	if s.workspace != nil {
		return s.workspace.Close()
	}
	return nil
}
func (s *Server) Health() Health {
	h := Ready()
	s.mu.Lock()
	stopping := s.stopping
	s.mu.Unlock()
	if stopping {
		h.Status = "stopping"
	}
	h.Idle = s.active.Load() == 0 && s.tasks.Active() == 0
	h.SafeToSuspend = h.Idle && !stopping
	if s.db == nil {
		return h
	}
	var pending int
	e := s.db.QueryRow(`SELECT (SELECT count(*) FROM task_attempts WHERE state IN ('approved','submitting','submitted','running','unknown','collection_pending','collecting'))+(SELECT count(*) FROM file_operations WHERE stage='prepared')+(SELECT count(*) FROM deliveries WHERE status='unknown')`).Scan(&pending)
	h.SafeToSuspend = h.SafeToSuspend && e == nil && pending == 0
	return h
}
func New(origin string, db *sql.DB, workspaceRoot string) (*Server, error) {
	ws, err := workspace.Open(workspaceRoot)
	if err != nil {
		return nil, err
	}

	secrets, err := providers.OpenSecrets(filepath.Join(filepath.Dir(workspaceRoot), "credentials"))
	if err != nil {
		ws.Close()
		return nil, err
	}
	srv := newHandler(origin, db, workspace.NewService(db, ws), providers.NewService(db, secrets)).(*Server)
	srv.workspace = ws
	if db != nil {
		if err = srv.tasks.Recover(context.Background()); err != nil {
			srv.Close()
			return nil, err
		}
	}
	return srv, nil
}
func newHandler(origin string, db *sql.DB, files *workspace.Service, configured ...*providers.Service) http.Handler {
	var providerService *providers.Service
	if len(configured) > 0 {
		providerService = configured[0]
	}
	if providerService != nil {
		providerService.Files = files
	}
	var deliveries *delivery.Service
	if files != nil {
		deliveries = delivery.NewService(db, files.Workspace, "user")
	}
	ev := events.NewService(db)
	srv := &Server{db: db, events: ev, tasks: tasks.NewService(db, ev, nil)}
	if providerService != nil {
		srv.tasks.Executor = tasks.ImageExecutor(providerService)
	}
	srv.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		srv.mu.Lock()
		if srv.stopping && r.URL.Path != "/api/health" {
			srv.mu.Unlock()
			reject(w, 503, "upstream", "Gateway is stopping")
			return
		}
		srv.requests.Add(1)
		srv.mu.Unlock()
		defer srv.requests.Done()
		if r.Method != "OPTIONS" && (r.Method == "POST" || fileRoute(r.URL.Path)) {
			srv.active.Add(1)
			defer srv.active.Add(-1)
		}
		if r.Method == http.MethodOptions {
			method := r.Header.Get("Access-Control-Request-Method")
			allowed := generationMethod(r.URL.Path, method) || (r.URL.Path == "/api/events" && method == "GET") || taskMethod(r.URL.Path, method) || (r.URL.Path == "/api/health" && method == "GET") || (r.URL.Path == "/api/providers" && (method == "GET" || method == "POST")) || (r.URL.Path == "/api/providers/receipt" && method == "GET") || (r.URL.Path == "/api/deliver" && method == "POST") || (kind != "" && (method == "GET" || method == "POST")) || (fileRoute(r.URL.Path) && fileMethod(r.URL.Path, method)) || (canvasRoute(r.URL.Path) && canvasMethod(r.URL.Path, method))
			if !allowed || !allowedPreflightHeaders(r.Header.Values("Access-Control-Request-Headers")) {
				reject(w, http.StatusForbidden, "invalid_input", "Preflight is not allowed")
				return
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Last-Event-ID, Range")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path == "/api/events" {
			serveEvents(w, r, srv.events)
			return
		}
		if generationRoute(r.URL.Path) {
			serveGenerate(w, r, providerService, srv.tasks)
			return
		}
		if taskRoute(r.URL.Path) {
			serveTasks(w, r, srv.tasks)
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
		respond(w, http.StatusOK, srv.Health())
	})
	return srv
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
