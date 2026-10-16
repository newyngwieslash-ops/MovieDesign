package server

import (
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/questions"
	"net/http"
	"strings"
)

func questionRoute(path string) bool {
	if path == "/api/question" {
		return true
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/question/"), "/")
	return strings.HasPrefix(path, "/api/question/") && len(parts) == 2 && parts[0] != "" && (parts[1] == "reply" || parts[1] == "reject")
}
func questionMethod(path, method string) bool {
	return questionRoute(path) && (method == "POST" || path == "/api/question" && method == "GET")
}
func serveQuestions(w http.ResponseWriter, r *http.Request, s *questions.Service) {
	w.Header().Set("Cache-Control", "no-store")
	if !questionMethod(r.URL.Path, r.Method) {
		reject(w, 405, "invalid_input", "Method not allowed")
		return
	}
	scope, e := scopedQuery(r, "project_id session_id")
	if e != nil {
		reject(w, 400, "invalid_input", e.Error())
		return
	}
	p, session := scope.Get("project_id"), scope.Get("session_id")
	if r.Method == "GET" {
		items, e := s.List(r.Context(), p, session)
		if e != nil {
			generationError(w, e)
			return
		}
		respond(w, 200, map[string]any{"items": items})
		return
	}
	if r.URL.Path == "/api/question" {
		// Public creation cannot provide prices, approval state, source or actor.
		in, e := DecodeJSON[struct {
			RequestID    string           `json:"requestId"`
			SessionID    string           `json:"session_id"`
			TaskID       string           `json:"task_id,omitempty"`
			Kind         string           `json:"kind"`
			TaskRevision int64            `json:"task_revision,omitempty"`
			Questions    []questions.Info `json:"questions,omitempty"`
		}](w, r)
		if e != nil || scope.Has("session_id") && session != in.SessionID {
			reject(w, 400, "invalid_input", "Invalid question schema/scope")
			return
		}
		q, e := s.Ask(r.Context(), p, questions.Question{RequestID: in.RequestID, SessionID: in.SessionID, TaskID: in.TaskID, Kind: in.Kind, TaskRevision: in.TaskRevision, Questions: in.Questions})
		if e != nil {
			generationError(w, e)
			return
		}
		respond(w, 200, q)
		return
	}
	in, e := DecodeJSON[questions.HumanReply](w, r)
	if e != nil {
		reject(w, 400, "invalid_input", "Invalid human reply schema")
		return
	}
	in.SessionID = session
	in.Reject = strings.HasSuffix(r.URL.Path, "/reject")
	id := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/question/"), "/")[0]
	receipt, e := s.Reply(r.Context(), p, id, in)
	if e != nil {
		generationError(w, e)
		return
	}
	respond(w, 200, receipt)
}
