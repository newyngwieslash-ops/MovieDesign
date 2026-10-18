package server

import (
	"database/sql"
	"errors"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/agent"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"io"
	"net/http"
	"net/url"
)

func agentMethod(path, method string) bool {
	return (path == "/api/agent/settings" && (method == "GET" || method == "POST")) || (path == "/api/agent/settings/receipt" || path == "/api/agent/status") && method == "GET"
}
func (s *Server) StartAgentHost(dir string, preparationFailed bool) error {
	s.agentPreparationFailed = preparationFailed
	if preparationFailed || dir == "" {
		return nil
	}
	s.agentBootstrap = dir
	h, err := agent.NewHost(s.llm, dir, s.mcp.Connection())
	if err != nil {
		return err
	}
	s.agentHost = h
	s.turns.Abort = h.Abort
	return nil
}
func (s *Server) serveAgent(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !agentMethod(r.URL.Path, r.Method) {
		reject(w, 405, "invalid_input", "Method not allowed")
		return
	}
	if s.llm == nil {
		reject(w, 503, "upstream", "LLM settings unavailable")
		return
	}
	var value any
	var err error
	if r.URL.Path == "/api/agent/settings/receipt" {
		q, e := url.ParseQuery(r.URL.RawQuery)
		if e != nil || len(q) != 1 || len(q["request_id"]) != 1 {
			reject(w, 400, "invalid_input", "One request_id required")
			return
		}
		value, err = s.llm.Receipt(r.Context(), q.Get("request_id"))
	} else if r.URL.RawQuery != "" {
		reject(w, 400, "invalid_input", "Agent settings accept no query")
		return
	} else if r.URL.Path == "/api/agent/status" {
		if s.agentPreparationFailed {
			var settings agent.View
			settings, _, err = s.llm.Read(r.Context())
			value = agent.Status{Configured: settings.Config != nil, Message: "agent 配置目录准备失败；请检查应用数据目录权限后重启。工作区仍可使用。"}
		} else {
			status := s.agentHost.Status(r.Context(), s.llm, s.agentBootstrap)
			status.ToolsReady = status.HostReady && s.agentHost.ToolsConnected(r.Context()) && s.mcp.CatalogReady()
			if status.ToolsReady {
				status.Message = "受管运行时与工具连接已就绪；聊天推理尚未开放。"
			}
			value = status
		}
	} else if r.Method == "GET" {
		value, _, err = s.llm.Read(r.Context())
	} else {
		var raw []byte
		raw, err = io.ReadAll(http.MaxBytesReader(w, r.Body, maxJSONBody))
		if err != nil {
			reject(w, 400, "invalid_input", "Invalid LLM request")
			return
		}
		value, err = s.llm.Execute(r.Context(), raw)
	}
	if err != nil {
		switch {
		case errors.Is(err, agent.ErrInvalid):
			reject(w, 400, "invalid_input", "Invalid LLM settings or request")
		case errors.Is(err, store.ErrRequestConflict), errors.Is(err, store.ErrRequestPending):
			reject(w, 409, "invalid_input", "requestId conflicts with existing intent")
		case errors.Is(err, sql.ErrNoRows):
			reject(w, 404, "not_found", "Original receipt not available; replay only the same intent")
		default:
			reject(w, 502, "upstream", "LLM operation failed")
		}
		return
	}
	respond(w, 200, value)
}
