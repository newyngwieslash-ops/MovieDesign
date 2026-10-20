package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/events"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/updates"
)

func updateRoute(path string) bool {
	return path == "/api/updates/policy" || path == "/api/updates/check"
}

func updateMethod(path, method string) bool {
	return (path == "/api/updates/policy" && (method == http.MethodGet || method == http.MethodPost)) ||
		(path == "/api/updates/check" && method == http.MethodPost)
}

type updateCheckRequest struct {
	RequestID string  `json:"requestId" validate:"required,max=128"`
	Version   *string `json:"version,omitempty"`
	Platform  *string `json:"platform,omitempty"`
	Locale    *string `json:"locale,omitempty"`
	Refresh   *bool   `json:"refresh,omitempty"`
}

func (s *Server) Updates() *updates.Service {
	return s.updates
}

func (s *Server) updateSafetyState(excludeSelfActive bool) (bool, string) {
	h := s.Health()
	if excludeSelfActive && !h.Idle && s.active.Load() <= 1 && s.tasks.Active() == 0 {
		turnIdle := s.turns == nil || s.turns.Current().TurnID == ""
		if turnIdle {
			h.Idle = true
			if h.Status == "ok" && s.db != nil {
				var pending int
				err := s.db.QueryRow(`SELECT (SELECT count(*) FROM task_attempts WHERE state IN ('approved','submitting','submitted','running','unknown','collection_pending','collecting','adoption_pending'))+(SELECT count(*) FROM file_operations WHERE stage='prepared')+(SELECT count(*) FROM deliveries WHERE status='unknown')+(SELECT count(*) FROM chat_dispatch WHERE terminal=0)+(SELECT count(*) FROM chat_turns WHERE state='active')+(SELECT count(*) FROM write_requests WHERE state='pending')`).Scan(&pending)
				h.SafeToSuspend = err == nil && pending == 0
			} else if h.Status == "ok" {
				h.SafeToSuspend = true
			}
		}
	}
	safe := h.Status == "ok" && h.Idle && h.SafeToSuspend
	if safe {
		return true, ""
	}
	return false, "存在正在运行的生成任务、聊天轮次或未完成操作，活动期间禁止自动安装更新或重启。"
}

func (s *Server) serveUpdates(w http.ResponseWriter, r *http.Request) {
	if !updateMethod(r.URL.Path, r.Method) {
		if r.URL.Path == "/api/updates/policy" {
			w.Header().Set("Allow", "GET, POST")
		} else {
			w.Header().Set("Allow", "POST")
		}
		reject(w, http.StatusMethodNotAllowed, "invalid_input", "Method not allowed")
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		reject(w, http.StatusBadRequest, "invalid_input", "Invalid update query")
		return
	}

	if r.Method == http.MethodGet {
		for key, values := range q {
			if len(values) != 1 || (key != "version" && key != "platform" && key != "locale" && key != "refresh" && key != "project_id") {
				reject(w, http.StatusBadRequest, "invalid_input", "Unknown or repeated query parameter")
				return
			}
		}
		if p := q.Get("project_id"); p != "" {
			if (store.Scope{ProjectID: p, Operation: "updates.policy", RequestID: "read"}).Validate() != nil || p == "_app" {
				reject(w, http.StatusBadRequest, "invalid_input", "Invalid project_id")
				return
			}
		}
		version := strings.TrimSpace(q.Get("version"))
		if version != "" && !updates.ValidSemver(version) {
			reject(w, http.StatusBadRequest, "invalid_input", "Invalid version")
			return
		}
		refresh := false
		if rawRefresh := strings.TrimSpace(q.Get("refresh")); rawRefresh != "" {
			switch strings.ToLower(rawRefresh) {
			case "1", "true":
				refresh = true
			case "0", "false":
				refresh = false
			default:
				reject(w, http.StatusBadRequest, "invalid_input", "Invalid refresh parameter")
				return
			}
		}
		safe, busyReason := s.updateSafetyState(false)
		snap := s.updates.Evaluate(r.Context(), updates.EvalOptions{
			CurrentVersion: version,
			Platform:       q.Get("platform"),
			Locale:         q.Get("locale"),
			PreferCache:    !refresh,
			ForceRefresh:   refresh,
			SafeToInstall:  safe,
			BusyReason:     busyReason,
		})
		respond(w, http.StatusOK, snap)
		return
	}

	// POST /api/updates/check or POST /api/updates/policy
	for key, values := range q {
		if len(values) != 1 || key != "project_id" {
			reject(w, http.StatusBadRequest, "invalid_input", "Unknown or repeated query parameter")
			return
		}
	}
	projectID := q.Get("project_id")
	scopeProject := "_app"
	if projectID != "" {
		if (store.Scope{ProjectID: projectID, Operation: "updates.check", RequestID: "check"}).Validate() != nil || projectID == "_app" {
			reject(w, http.StatusBadRequest, "invalid_input", "Invalid project_id")
			return
		}
		scopeProject = projectID
	}

	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxJSONBody))
	if err != nil {
		var large *http.MaxBytesError
		if errors.As(err, &large) {
			objectError(w, err)
		} else {
			reject(w, http.StatusBadRequest, "invalid_input", "Invalid request body")
		}
		return
	}
	req, err := store.DecodeValue[updateCheckRequest](raw)
	if err != nil {
		reject(w, http.StatusBadRequest, "invalid_input", "Invalid update check request")
		return
	}
	scope := store.Scope{
		ProjectID: scopeProject,
		Operation: "updates.check",
		RequestID: req.RequestID,
	}
	if err := scope.Validate(); err != nil {
		reject(w, http.StatusBadRequest, "invalid_input", "Invalid requestId or project_id")
		return
	}
	version := updates.DefaultAppVersion
	if req.Version != nil {
		trimmed := strings.TrimSpace(*req.Version)
		if trimmed == "" || !updates.ValidSemver(trimmed) {
			reject(w, http.StatusBadRequest, "invalid_input", "Invalid version")
			return
		}
		version = trimmed
	}
	platform := updates.DefaultPlatform
	if req.Platform != nil {
		trimmed := strings.TrimSpace(*req.Platform)
		if trimmed == "" {
			reject(w, http.StatusBadRequest, "invalid_input", "Invalid platform")
			return
		}
		platform = trimmed
	}
	locale := updates.DefaultLocale
	if req.Locale != nil {
		trimmed := strings.TrimSpace(*req.Locale)
		if trimmed == "" {
			reject(w, http.StatusBadRequest, "invalid_input", "Invalid locale")
			return
		}
		locale = trimmed
	}
	forceRefresh := true
	if req.Refresh != nil {
		forceRefresh = *req.Refresh
	}

	if s.db != nil {
		canonical, err := store.CanonicalJSON(raw)
		if err != nil {
			reject(w, http.StatusBadRequest, "invalid_input", "Invalid JSON")
			return
		}
		if projectID != "" {
			var exists string
			if err := s.db.QueryRowContext(r.Context(), `SELECT id FROM projects WHERE id=?`, projectID).Scan(&exists); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					reject(w, http.StatusNotFound, "not_found", "Project not found")
					return
				}
				objectError(w, err)
				return
			}
		}
		var prevInput, prevState string
		var prevResp sql.NullString
		err = s.db.QueryRowContext(r.Context(), `SELECT input_json,state,response_json FROM write_requests WHERE project_id=? AND operation=? AND request_id=?`, scope.ProjectID, scope.Operation, scope.RequestID).Scan(&prevInput, &prevState, &prevResp)
		if err == nil {
			if prevInput != string(canonical) {
				objectError(w, store.ErrRequestConflict)
				return
			}
			if prevState != "completed" || !prevResp.Valid {
				objectError(w, store.ErrRequestPending)
				return
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(prevResp.String))
			return
		}
	}

	safe, busyReason := s.updateSafetyState(true)
	snap := s.updates.Evaluate(r.Context(), updates.EvalOptions{
		CurrentVersion: version,
		Platform:       platform,
		Locale:         locale,
		PreferCache:    !forceRefresh,
		ForceRefresh:   forceRefresh,
		SafeToInstall:  safe,
		BusyReason:     busyReason,
	})
	encodedSnap, err := json.Marshal(snap)
	if err != nil {
		reject(w, http.StatusBadGateway, "upstream", "Failed to encode update snapshot")
		return
	}

	if s.db == nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(encodedSnap)
		return
	}

	eventPayload, err := json.Marshal(map[string]any{
		"requestId":            req.RequestID,
		"status":               snap.Status,
		"source":               snap.Source,
		"stale":                snap.Stale,
		"currentVersion":       snap.CurrentVersion,
		"platform":             snap.Platform,
		"forced":               snap.Resolved.Forced,
		"manualOnly":           snap.Resolved.ManualOnly,
		"minSupportedVersion":  snap.Resolved.MinSupportedVersion,
		"requiredReason":       snap.Resolved.RequiredReason,
		"manualDownloadUrl":    snap.Resolved.ManualDownloadURL,
		"policyFallbackReason": snap.PolicyFallbackReason,
		"busy":                 snap.Busy,
		"autoUpdateAllowed":    snap.AutoUpdateAllowed,
	})
	if err != nil {
		reject(w, http.StatusBadGateway, "upstream", "Failed to encode update event")
		return
	}

	out, err := store.WriteRequest(r.Context(), s.db, scope, raw, func(tx *sql.Tx) ([]byte, error) {
		if projectID != "" {
			if err := events.Append(r.Context(), tx, projectID, "updates", "", "app_update_check", eventPayload); err != nil {
				return nil, err
			}
		} else {
			if _, err := tx.ExecContext(r.Context(), `INSERT INTO events(project_id,session_id,task_id,type,payload_json,created_at) VALUES(NULL,'updates',NULL,'app_update_check',?,?)`, string(eventPayload), time.Now().UnixMilli()); err != nil {
				return nil, err
			}
		}
		return encodedSnap, nil
	})
	if err != nil {
		objectError(w, err)
		return
	}
	if s.events != nil {
		s.events.Notify()
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}
