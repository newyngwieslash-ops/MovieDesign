package server

import (
	"errors"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/delivery"
	"net/http"
	"net/url"
)

func serveDelivery(w http.ResponseWriter, r *http.Request, s *delivery.Service) {
	if r.Method != "POST" {
		w.Header().Set("Allow", "POST")
		reject(w, 405, "invalid_input", "Method not allowed")
		return
	}
	q, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil {
		reject(w, 400, "invalid_input", "Malformed query")
		return
	}
	for k, v := range q {
		if k != "project_id" || len(v) != 1 {
			reject(w, 400, "invalid_input", "Unknown or repeated query parameter")
			return
		}
	}
	p := q.Get("project_id")
	if s == nil {
		reject(w, 503, "upstream", "Delivery service unavailable")
		return
	}
	in, e := DecodeJSON[delivery.Request](w, r)
	if e != nil {
		var large *http.MaxBytesError
		if errors.As(e, &large) {
			objectError(w, e)
		} else {
			reject(w, 400, "invalid_input", e.Error())
		}
		return
	}
	out, e := s.Deliver(r.Context(), p, in)
	if e != nil {
		objectError(w, e)
		return
	}
	respond(w, 200, out)
}
