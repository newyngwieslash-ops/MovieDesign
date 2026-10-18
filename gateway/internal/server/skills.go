package server

import (
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"net/http"
	"net/url"
	"strconv"
)

func (s *Server) serveSkills(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		w.Header().Set("Allow", "GET")
		reject(w, 405, "invalid_input", "Method not allowed")
		return
	}
	q, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil {
		reject(w, 400, "invalid_input", "Invalid skill query")
		return
	}
	for key, values := range q {
		if len(values) != 1 || (key != "name" && key != "offset" && key != "limit") {
			reject(w, 400, "invalid_input", "Invalid skill query")
			return
		}
	}
	if q.Has("name") && (q.Get("name") == "" || len(q) != 1) {
		reject(w, 400, "invalid_input", "Invalid skill query")
		return
	}
	if s.chat.Skills == nil {
		reject(w, 503, "upstream", "Skills unavailable")
		return
	}
	if name := q.Get("name"); name != "" {
		skill, e := s.chat.Skills.Get(name)
		if e != nil {
			generationError(w, e)
			return
		}
		respond(w, 200, skill)
		return
	}
	list, e := s.chat.Skills.List()
	if e != nil {
		generationError(w, e)
		return
	}
	offset, limit := 0, 50
	for key, target := range map[string]*int{"offset": &offset, "limit": &limit} {
		if q.Has(key) {
			v, e := strconv.Atoi(q.Get(key))
			if e != nil || strconv.Itoa(v) != q.Get(key) || v < 0 || key == "limit" && (v < 1 || v > 200) {
				reject(w, 400, "invalid_input", "Invalid pagination")
				return
			}
			*target = v
		}
	}
	total := len(list)
	start := min(offset, total)
	end := start + min(limit, total-start)
	respond(w, 200, map[string]any{"items": list[start:end], "coverage": store.Coverage{Total: total, NextOffset: offset + end - start, Exhausted: end == total}})
}
