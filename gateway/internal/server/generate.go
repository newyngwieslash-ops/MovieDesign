package server

import (
	"errors"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/generation"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"

	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

func generationMethod(path, method string) bool {
	return (path == "/api/generate/image" || path == "/api/generate/text" || path == "/api/generate/estimate" || path == "/api/generate/mask" || path == "/api/v3/capabilities/get") && method == "POST" || (path == "/api/v3/capabilities" || path == "/api/knowledge/providers/openai-images.md" || path == "/api/knowledge/providers/text.md") && method == "GET"
}
func generationRoute(path string) bool {
	return generationMethod(path, "GET") || generationMethod(path, "POST")
}
func generationError(w http.ResponseWriter, e error) {
	if errors.Is(e, providers.ErrInvalid) {
		reject(w, 400, "invalid_input", e.Error())
		return
	}
	if errors.Is(e, providers.ErrNotFound) {
		reject(w, 404, "not_found", "Capability or quote not found")
		return
	}
	objectError(w, e)
}
func serveGenerate(w http.ResponseWriter, r *http.Request, service generation.Service) {
	ps := service.Providers
	w.Header().Set("Cache-Control", "no-store")
	if !generationMethod(r.URL.Path, r.Method) {
		reject(w, 405, "invalid_input", "Method not allowed")
		return
	}
	if ps == nil {
		reject(w, 503, "upstream", "Provider service unavailable")
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/v3/capabilities") || strings.HasPrefix(r.URL.Path, "/api/knowledge/") {
		serveCapabilities(w, r, service)
		return
	}
	q, e := scopedQuery(r, "project_id")
	if e != nil {
		reject(w, 400, "invalid_input", e.Error())
		return
	}
	p := q.Get("project_id")
	if r.URL.Path == "/api/generate/mask" {
		serveMask(w, r, ps, p)
		return
	}
	raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, maxJSONBody))
	if e != nil {
		reject(w, 400, "invalid_input", "Invalid generation body")
		return
	}
	request, e := store.DecodeValue[generation.Request](raw)
	if e != nil {
		reject(w, 400, "invalid_input", "Invalid generation schema")
		return
	}
	if r.URL.Path == "/api/generate/text" && request.CapabilityID != "text:configured-llm" || r.URL.Path == "/api/generate/image" && !strings.HasPrefix(request.CapabilityID, "image:") {
		reject(w, 400, "invalid_input", "Capability does not match generation route")
		return
	}
	result, e := service.Admit(r.Context(), p, request, raw, r.URL.Path == "/api/generate/estimate", "")
	if e != nil {
		generationError(w, e)
		return
	}
	if result.Quote != nil {
		respond(w, 200, result.Quote)
	} else {
		respond(w, 202, map[string]string{"task_id": result.Task.ID})
	}
}

func serveCapabilities(w http.ResponseWriter, r *http.Request, s generation.Service) {
	if r.URL.Path == "/api/knowledge/providers/openai-images.md" || r.URL.Path == "/api/knowledge/providers/text.md" {
		if r.URL.RawQuery != "" {
			reject(w, 400, "invalid_input", "Knowledge card accepts no query")
			return
		}
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		card := providers.ImageKnowledgeCard
		if strings.HasSuffix(r.URL.Path, "/text.md") {
			card = providers.TextKnowledgeCard
		}
		w.Write([]byte(card))
		return
	}
	caps, e := s.Capabilities(r.Context())
	if e != nil {
		generationError(w, e)
		return
	}
	if r.URL.Path == "/api/v3/capabilities/get" {
		if r.URL.RawQuery != "" {
			reject(w, 400, "invalid_input", "Batch lookup accepts no query")
			return
		}
		in, e := DecodeJSON[struct {
			IDs []string `json:"ids"`
		}](w, r)
		if e != nil || len(in.IDs) < 1 || len(in.IDs) > 20 {
			reject(w, 400, "invalid_input", "Expected 1-20 capability IDs")
			return
		}
		result := []providers.Capability{}
		unavailable := []map[string]string{}
		seen := map[string]bool{}
		for _, id := range in.IDs {
			if seen[id] {
				continue
			}
			seen[id] = true
			found := false
			for _, cap := range caps {
				if cap.ID == id {
					result = append(result, cap)
					found = true
					break
				}
			}
			if !found {
				unavailable = append(unavailable, map[string]string{"id": id, "reason": "unavailable"})
			}
		}
		respond(w, 200, map[string]any{"capabilities": result, "unavailable": unavailable})
		return
	}
	q, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil {
		reject(w, 400, "invalid_input", "Invalid catalog query")
		return
	}
	for k, v := range q {
		if !slices.Contains([]string{"operation", "offset", "limit"}, k) || k != "operation" && len(v) != 1 {
			reject(w, 400, "invalid_input", "Unknown or repeated catalog query")
			return
		}
	}
	for _, op := range q["operation"] {
		if !slices.Contains([]string{"image", "video", "text"}, op) {
			reject(w, 400, "invalid_input", "Unsupported operation")
			return
		}
	}
	if len(q["operation"]) > 0 {
		caps = slices.DeleteFunc(caps, func(c providers.Capability) bool { return !slices.Contains(q["operation"], c.Operation) })
	}
	offset, limit := 0, 50
	for k, target := range map[string]*int{"offset": &offset, "limit": &limit} {
		if q.Has(k) {
			v, e := strconv.Atoi(q.Get(k))
			if e != nil || strconv.Itoa(v) != q.Get(k) || v < 0 || k == "limit" && (v < 1 || v > 100) {
				reject(w, 400, "invalid_input", "Invalid pagination")
				return
			}
			*target = v
		}
	}
	total := len(caps)
	start := min(offset, total)
	end := min(start+limit, total)
	respond(w, 200, map[string]any{"capabilities": caps[start:end], "coverage": store.Coverage{Total: total, NextOffset: end, Exhausted: end == total}})
}
