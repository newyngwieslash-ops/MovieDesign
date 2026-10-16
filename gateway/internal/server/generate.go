package server

import (
	"encoding/json"
	"errors"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/delivery"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/tasks"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

type generationRequest struct {
	RequestID    string          `json:"requestId"`
	SessionID    string          `json:"session_id,omitempty"`
	CapabilityID string          `json:"capability_id"`
	Prompt       string          `json:"prompt"`
	Params       map[string]any  `json:"params"`
	References   []string        `json:"references"`
	Count        *int            `json:"count,omitempty"`
	Output       delivery.Output `json:"output"`
	QuoteID      *string         `json:"quote_id,omitempty"`
}

func generationMethod(path, method string) bool {
	return (path == "/api/generate/image" || path == "/api/generate/estimate" || path == "/api/generate/mask" || path == "/api/v3/capabilities/get") && method == "POST" || (path == "/api/v3/capabilities" || path == "/api/knowledge/providers/openai-images.md") && method == "GET"
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
func serveGenerate(w http.ResponseWriter, r *http.Request, ps *providers.Service, ts *tasks.Service) {
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
		serveCapabilities(w, r, ps)
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
	request, e := store.DecodeValue[generationRequest](raw)
	if e != nil {
		reject(w, 400, "invalid_input", "Invalid generation schema")
		return
	}
	estimate := r.URL.Path == "/api/generate/estimate"
	if estimate {
		previous, found, err := ps.ReplayQuote(r.Context(), p, request.RequestID, raw)
		if err != nil {
			generationError(w, err)
			return
		}
		if found {
			respond(w, 200, previous)
			return
		}
	} else {
		previous, found, err := ts.ReplaySubmission(r.Context(), p, request.RequestID, raw)
		if err != nil {
			generationError(w, err)
			return
		}
		if found {
			respond(w, 202, map[string]string{"task_id": previous.ID})
			return
		}
	}
	var fields map[string]json.RawMessage
	json.Unmarshal(raw, &fields)
	for _, key := range []string{"prompt", "params", "references", "output", "count", "quote_id", "session_id"} {
		if b, ok := fields[key]; ok && string(b) == "null" {
			reject(w, 400, "invalid_input", key+" cannot be null")
			return
		}
	}
	count := 1
	if request.Params == nil {
		request.Params = map[string]any{}
	}
	if request.References == nil {
		request.References = []string{}
	}
	if request.Count != nil {
		count = *request.Count
	}
	if !strings.HasPrefix(request.CapabilityID, "image:") || len(request.CapabilityID) <= 6 {
		reject(w, 400, "invalid_input", "Expected an explicit image capability ID")
		return
	}
	f, e := ps.Resolve(strings.TrimPrefix(request.CapabilityID, "image:"))
	if e != nil {
		generationError(w, e)
		return
	}
	cap := providers.ImageCapability(f.Config)
	if f.Config.Kind != "image" {
		reject(w, 400, "invalid_input", "Unsupported image capability")
		return
	}
	output, e := json.Marshal(request.Output)
	if e != nil {
		generationError(w, e)
		return
	}
	input := providers.GenerationInput{ProjectID: p, Prompt: request.Prompt, Params: request.Params, References: request.References, Count: count, Output: output}
	if e = providers.Validate(cap, input); e != nil {
		generationError(w, e)
		return
	}
	input.Assets, e = ps.FreezeReferences(r.Context(), p, input, f)
	if e != nil {
		generationError(w, e)
		return
	}
	params, _ := json.Marshal(request.Params)
	task := tasks.Input{RequestID: request.RequestID, SessionID: request.SessionID, CapabilityID: request.CapabilityID, Prompt: request.Prompt, Params: params, Output: request.Output, Count: count, Assets: input.Assets, Provider: f, OriginalWire: raw}
	for i, id := range request.References {
		task.References = append(task.References, tasks.Reference{AssetID: id, Role: providers.ReferenceRole(cap, i)})
	}
	if e = ts.ValidateAdmission(r.Context(), p, task); e != nil {
		generationError(w, e)
		return
	}
	quote, e := providers.Estimate(input, f)
	if e != nil {
		generationError(w, e)
		return
	}
	if estimate {
		if request.QuoteID != nil {
			reject(w, 400, "invalid_input", "Estimate does not accept quote_id")
			return
		}
		quote, e = ps.SaveQuote(r.Context(), p, request.RequestID, raw, input, f)
		if e != nil {
			generationError(w, e)
			return
		}
		respond(w, 200, quote)
		return
	}
	if request.QuoteID != nil {
		saved, err := ps.QuoteBinding(r.Context(), p, *request.QuoteID)
		if err != nil {
			generationError(w, err)
			return
		}
		if saved.Quote.InputHash != quote.InputHash || saved.Quote.ExpiresAt <= time.Now().UnixMilli() {
			reject(w, 409, "waiting_user", "Quote expired or input/configuration changed; estimate again")
			return
		}
		task.QuoteID = saved.Quote.QuoteID
	}
	result, e := ts.Submit(r.Context(), p, task)
	if e != nil {
		generationError(w, e)
		return
	}
	respond(w, 202, map[string]string{"task_id": result.ID})
}
func serveCapabilities(w http.ResponseWriter, r *http.Request, s *providers.Service) {
	if r.URL.Path == "/api/knowledge/providers/openai-images.md" {
		if r.URL.RawQuery != "" {
			reject(w, 400, "invalid_input", "Knowledge card accepts no query")
			return
		}
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Write([]byte(providers.ImageKnowledgeCard))
		return
	}
	items, e := s.List(r.Context())
	if e != nil {
		generationError(w, e)
		return
	}
	caps := []providers.Capability{}
	for _, v := range items {
		if v.Config.Enabled && v.Config.Kind == "image" {
			caps = append(caps, providers.ImageCapability(v.Config))
		}
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
	if len(q["operation"]) > 0 && !slices.Contains(q["operation"], "image") {
		caps = []providers.Capability{}
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
