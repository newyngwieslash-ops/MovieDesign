package server

import (
	"errors"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/delivery"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/pipeline"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"io"
	"net/http"
	"net/url"
)

func pipelineRoute(path string) bool {
	return path == "/api/pipeline/storyboard" || path == "/api/pipeline/artifact" || path == "/api/pipeline/prompt" || path == "/api/pipeline/lineage" || path == "/api/pipeline/preflight" || path == "/api/pipeline/compile" || path == "/api/documents/write"
}

func pipelineMethod(path, method string) bool {
	return (path == "/api/pipeline/storyboard" || path == "/api/pipeline/artifact" || path == "/api/pipeline/prompt" || path == "/api/pipeline/lineage") && method == "GET" || (path == "/api/documents/write" || path == "/api/pipeline/preflight" || path == "/api/pipeline/compile") && method == "POST"
}
func (s *Server) servePipeline(w http.ResponseWriter, r *http.Request, d *delivery.Service) {
	if !pipelineMethod(r.URL.Path, r.Method) {
		reject(w, 405, "invalid_input", "Method not allowed")
		return
	}
	q, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil {
		reject(w, 400, "invalid_input", "Invalid query")
		return
	}
	for key, values := range q {
		if len(values) != 1 || (key != "project_id" && (r.Method != "GET" || key != "versionId")) {
			reject(w, 400, "invalid_input", "Unknown or repeated query field")
			return
		}
	}
	p := q.Get("project_id")
	if (store.Scope{ProjectID: p, Operation: "storyboard", RequestID: "query"}).Validate() != nil || p == "_app" {
		reject(w, 400, "invalid_input", "Invalid project_id")
		return
	}
	if r.Method == "GET" {
		if q.Get("versionId") == "" {
			reject(w, 400, "invalid_input", "versionId is required")
			return
		}
		if r.URL.Path == "/api/pipeline/artifact" {
			b, e := pipeline.BindingForVersion(r.Context(), s.db, p, q.Get("versionId"))
			if e != nil {
				objectError(w, e)
				return
			}
			respond(w, 200, b)
			return
		}
		if r.URL.Path == "/api/pipeline/prompt" {
			v, e := pipeline.ReadPromptVersion(r.Context(), s.db, p, q.Get("versionId"))
			if e != nil {
				objectError(w, e)
				return
			}
			respond(w, 200, v)
			return
		}
		if r.URL.Path == "/api/pipeline/lineage" {
			l, e := pipeline.TraceLineage(r.Context(), s.db, p, q.Get("versionId"))
			if e != nil {
				objectError(w, e)
				return
			}
			respond(w, 200, l)
			return
		}
		v, e := pipeline.ReadVersion(r.Context(), s.db, p, q.Get("versionId"))
		if e != nil {
			objectError(w, e)
			return
		}
		respond(w, 200, v)
		return
	}
	raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, maxJSONBody))
	if e != nil {
		var large *http.MaxBytesError
		if errors.As(e, &large) {
			objectError(w, e)
		} else {
			reject(w, 400, "invalid_input", "Invalid document body")
		}
		return
	}
	if r.URL.Path == "/api/pipeline/preflight" || r.URL.Path == "/api/pipeline/compile" {
		in, e := store.DecodeValue[pipeline.CompileInput](raw)
		if e != nil {
			reject(w, 400, "invalid_input", "Invalid preflight or compile schema")
			return
		}
		if r.URL.Path == "/api/pipeline/preflight" {
			report, e := pipeline.Preflight(r.Context(), s.db, p, in)
			if e != nil {
				objectError(w, e)
				return
			}
			respond(w, 200, report)
			return
		}
		contract, report, e := pipeline.Compile(r.Context(), s.db, p, in)
		if e != nil {
			objectError(w, e)
			return
		}
		if s.events != nil {
			s.events.Notify()
		}
		respond(w, 200, map[string]any{"contract": contract, "preflight": report})
		return
	}
	// Shared WriteDocument strictly decodes the unchanged wire, preserving omitted
	// versus explicit fields for durable replay across HTTP and MCP.
	if d == nil {
		reject(w, 503, "upstream", "Documents unavailable")
		return
	}
	result, e := d.WriteDocument(r.Context(), p, raw)
	if e != nil {
		objectError(w, e)
		return
	}
	respond(w, 200, result)
}
