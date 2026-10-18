package server

import (
	"encoding/json"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"io"
	"net/http"
	"strconv"
	"strings"
)

func chatMethod(path, method string) bool {
	if path == "/api/chat/sessions" {
		return method == "GET" || method == "POST"
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/chat/sessions/"), "/")
	return strings.HasPrefix(path, "/api/chat/sessions/") && len(parts) == 2 && parts[0] != "" && (parts[1] == "messages" && (method == "GET" || method == "POST") || parts[1] == "cancel" && method == "POST")
}
func (s *Server) serveChat(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !chatMethod(r.URL.Path, r.Method) {
		reject(w, 405, "invalid_input", "Method not allowed")
		return
	}
	allowed := "project_id"
	if r.Method == "GET" {
		allowed += " offset limit"
	}
	scope, e := scopedQuery(r, allowed)
	if e != nil {
		reject(w, 400, "invalid_input", "Invalid scope")
		return
	}
	p := scope.Get("project_id")
	var value any
	status := 200
	if r.Method == "GET" {
		offset, limit := 0, 50
		for key, target := range map[string]*int{"offset": &offset, "limit": &limit} {
			if scope.Has(key) {
				v, e := strconv.Atoi(scope.Get(key))
				if e != nil || strconv.Itoa(v) != scope.Get(key) || v < 0 || key == "limit" && (v < 1 || v > 100) {
					reject(w, 400, "invalid_input", "Invalid pagination")
					return
				}
				*target = v
			}
		}
		var total int
		if r.URL.Path == "/api/chat/sessions" {
			value, e = s.chat.List(r.Context(), p, offset, limit)
			if e == nil {
				e = s.db.QueryRowContext(r.Context(), "SELECT count(*) FROM chat_sessions WHERE project_id=?", p).Scan(&total)
			}
		} else {
			session := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/chat/sessions/"), "/")[0]
			value, e = s.chat.Messages(r.Context(), p, session, offset, limit)
			if e == nil {
				e = s.db.QueryRowContext(r.Context(), "SELECT count(*) FROM chat_messages WHERE session_id=?", session).Scan(&total)
			}
		}
		if e == nil {
			raw, _ := json.Marshal(value)
			page := store.Page{}
			e = json.Unmarshal(raw, &page.Items)
			page.Coverage = store.Coverage{Total: total, NextOffset: offset + len(page.Items), Exhausted: offset+len(page.Items) >= total}
			value = page
		}
	} else {
		raw, readErr := io.ReadAll(http.MaxBytesReader(w, r.Body, maxJSONBody))
		if readErr != nil {
			reject(w, 400, "invalid_input", "Invalid body")
			return
		}
		if r.URL.Path == "/api/chat/sessions" {
			value, e = s.chat.Create(r.Context(), p, raw)
		} else {
			parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/chat/sessions/"), "/")
			if parts[1] == "cancel" {
				value, e = s.chat.Cancel(r.Context(), p, parts[0], raw)
			} else {
				status = 202
				value, e = s.chat.Send(r.Context(), p, parts[0], raw)
			}
		}
	}
	if e != nil {
		generationError(w, e)
		return
	}
	respond(w, status, value)
}
