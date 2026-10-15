package server

import (
	"errors"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"io"
	"net/http"
	"net/url"
)

func serveProviders(w http.ResponseWriter, r *http.Request, s *providers.Service) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Path == "/api/providers/receipt" {
		if r.Method != "GET" {
			reject(w, 405, "invalid_input", "Method not allowed")
			return
		}
		q, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || len(q) != 1 || len(q["request_id"]) != 1 {
			reject(w, 400, "invalid_input", "One request_id is required")
			return
		}
		if s == nil {
			reject(w, 503, "upstream", "Provider storage unavailable")
			return
		}
		receipt, err := s.Receipt(r.Context(), q.Get("request_id"))
		if err != nil {
			if errors.Is(err, providers.ErrNotFound) {
				reject(w, 404, "not_found", "Original receipt not available; only replay the same intent")
			} else if errors.Is(err, providers.ErrInvalid) {
				reject(w, 400, "invalid_input", "Invalid request_id")
			} else {
				reject(w, 502, "upstream", "Receipt unavailable")
			}
			return
		}
		respond(w, 200, receipt)
		return
	}
	if r.URL.RawQuery != "" {
		reject(w, 400, "invalid_input", "Provider settings are application-scoped and accept no query")
		return
	}
	if r.Method != "GET" && r.Method != "POST" {
		reject(w, 405, "invalid_input", "Method not allowed")
		return
	}
	if s == nil {
		reject(w, 503, "upstream", "Provider storage unavailable")
		return
	}
	if r.Method == "GET" {
		items, err := s.List(r.Context())
		if err != nil {
			reject(w, 502, "upstream", "Provider storage unavailable")
			return
		}
		respond(w, 200, map[string]any{"items": items})
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxJSONBody))
	if err != nil {
		reject(w, 400, "invalid_input", "Invalid provider body")
		return
	}
	result, err := s.Execute(r.Context(), raw)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrRequestConflict), errors.Is(err, store.ErrRequestPending):
			reject(w, 409, "invalid_input", "requestId conflicts with an existing intent")
		case errors.Is(err, providers.ErrInvalid):
			reject(w, 400, "invalid_input", err.Error())
		case errors.Is(err, providers.ErrNotFound):
			reject(w, 404, "not_found", "Provider not found")
		default:
			reject(w, 502, "upstream", "Provider operation failed")
		}
		return
	}
	respond(w, 200, result)
}
