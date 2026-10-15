package server

import (
	"errors"
	"io"
	"net/http"
	"net/url"

	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
)

const maxCanvasBody = 16 << 20

func canvasRoute(path string) bool {
	return path == "/api/canvas/nodes" || path == "/api/canvas/commands" || path == "/api/canvas/content"
}
func canvasMethod(path, method string) bool {
	return path == "/api/canvas/nodes" && method == "GET" || (path == "/api/canvas/commands" || path == "/api/canvas/content") && method == "POST"
}
func canvasFailure(w http.ResponseWriter, e error) {
	var c *store.CanvasError
	var large *http.MaxBytesError
	if errors.As(e, &c) {
		respond(w, c.Status, map[string]any{"error": map[string]any{"code": "invalid_input", "message": c.Message, "details": map[string]string{"kind": c.Kind}}})
		return
	}
	if errors.As(e, &large) {
		respond(w, 413, map[string]any{"error": map[string]any{"code": "invalid_input", "message": "Canvas JSON body exceeds 16 MiB", "details": map[string]string{"kind": "CANVAS_V3_BODY_TOO_LARGE"}}})
		return
	}
	objectError(w, e)
}
func serveCanvas(w http.ResponseWriter, r *http.Request, s store.Canvas) {
	w.Header().Set("Cache-Control", "no-store")
	if !canvasMethod(r.URL.Path, r.Method) {
		reject(w, 405, "invalid_input", "Method not allowed")
		return
	}
	q, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil || len(q) != 1 || len(q["project_id"]) != 1 {
		reject(w, 400, "invalid_input", "Only one project_id is required")
		return
	}
	p := q.Get("project_id")
	if e = (store.Scope{ProjectID: p, RequestID: "query", Operation: "canvas"}).Validate(); e != nil || p == "_app" {
		reject(w, 400, "invalid_input", "Invalid project_id")
		return
	}
	if r.Method == "GET" {
		d, e := s.Document(r.Context(), p)
		if e != nil {
			canvasFailure(w, e)
			return
		}
		respond(w, 200, d)
		return
	}
	// The service applies the same canonical/duplicate/exact-field boundary for HTTP and future MCP.
	raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, maxCanvasBody))
	if e != nil {
		canvasFailure(w, e)
		return
	}
	if r.URL.Path == "/api/canvas/content" {
		v, e := s.Content(r.Context(), p, raw)
		if e != nil {
			canvasFailure(w, e)
			return
		}
		respond(w, 200, v)
		return
	}
	v, e := s.Execute(r.Context(), p, raw)
	if e != nil {
		canvasFailure(w, e)
		return
	}
	respond(w, 200, v)
}
