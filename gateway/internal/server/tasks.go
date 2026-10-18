package server

import (
	"context"
	"errors"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/tasks"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

func taskRoute(path string) bool {
	if path == "/api/tasks" {
		return true
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/tasks/"), "/")
	return strings.HasPrefix(path, "/api/tasks/") && parts[0] != "" && (len(parts) == 1 || len(parts) == 2 && parts[1] == "resolve")
}
func taskMethod(path, method string) bool {
	return taskRoute(path) && ((strings.HasSuffix(path, "/resolve") && method == "POST") || (!strings.HasSuffix(path, "/resolve") && method == "GET"))
}
func scopedQuery(r *http.Request, allowed string) (url.Values, error) {
	q, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil {
		return nil, e
	}
	for k, v := range q {
		if len(v) != 1 || !slices.Contains(strings.Fields(allowed), k) {
			return nil, errors.New("Invalid query parameters")
		}
	}
	p := q.Get("project_id")
	if e = (store.Scope{ProjectID: p, Operation: "query", RequestID: "query"}).Validate(); e != nil || p == "_app" {
		return nil, errors.New("Invalid project_id")
	}
	return q, nil
}
func serveTasks(w http.ResponseWriter, r *http.Request, s *tasks.Service) {
	w.Header().Set("Cache-Control", "no-store")
	if !taskMethod(r.URL.Path, r.Method) {
		reject(w, 405, "invalid_input", "Method not allowed")
		return
	}
	allowed := "project_id"
	if r.Method == "GET" {
		if r.URL.Path == "/api/tasks" {
			allowed += " session_id offset limit"
		} else {
			allowed += " after_revision timeout_seconds"
		}
	}
	q, e := scopedQuery(r, allowed)
	if e != nil {
		reject(w, 400, "invalid_input", e.Error())
		return
	}
	if r.URL.Path == "/api/tasks" {
		if r.Method != "GET" || q.Has("after_revision") || q.Has("timeout_seconds") {
			reject(w, 400, "invalid_input", "Invalid task list request")
			return
		}
		offset, limit := 0, 50
		for name, dest := range map[string]*int{"offset": &offset, "limit": &limit} {
			if q.Has(name) {
				n, err := strconv.Atoi(q.Get(name))
				if err != nil || strconv.Itoa(n) != q.Get(name) || n < 0 || name == "limit" && (n < 1 || n > 200) {
					reject(w, 400, "invalid_input", "Invalid pagination")
					return
				}
				*dest = n
			}
		}
		items, err := s.List(r.Context(), q.Get("project_id"), q.Get("session_id"), offset, limit)
		if err != nil {
			objectError(w, err)
			return
		}
		respond(w, 200, items)
		return
	}
	id := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/tasks/"), "/")[0]
	p := q.Get("project_id")
	var v tasks.TaskView
	if r.Method == "POST" {
		in, err := DecodeJSON[tasks.ResolveInput](w, r)
		if err != nil {
			reject(w, 400, "invalid_input", err.Error())
			return
		}
		v, e = s.Resolve(r.Context(), p, id, in)
		if e == nil && in.Action == "retry" {
			e = s.Dispatch(p, id)
		}
	} else if q.Has("after_revision") {
		after, err := strconv.ParseInt(q.Get("after_revision"), 10, 64)
		if err != nil || after < 1 || strconv.FormatInt(after, 10) != q.Get("after_revision") {
			reject(w, 400, "invalid_input", "Invalid revision")
			return
		}
		timeout := int64(30)
		if q.Has("timeout_seconds") {
			timeout, err = strconv.ParseInt(q.Get("timeout_seconds"), 10, 64)
			if err != nil || timeout < 0 || timeout > 60 || strconv.FormatInt(timeout, 10) != q.Get("timeout_seconds") {
				reject(w, 400, "invalid_input", "Invalid timeout")
				return
			}
		}
		ctx, cancel := context.WithTimeout(r.Context(), time.Duration(timeout)*time.Second)
		defer cancel()
		// Validate the cursor even when the observer timeout is zero.
		v, e = s.Query(r.Context(), p, id)
		if e != nil {
			objectError(w, e)
			return
		}
		if after > v.Revision {
			reject(w, 400, "invalid_input", "Revision is in the future")
			return
		}
		v, e = s.Changed(ctx, p, id, after)
		if errors.Is(e, context.DeadlineExceeded) {
			v, e = s.Query(r.Context(), p, id)
		}
	} else {
		if q.Has("timeout_seconds") {
			reject(w, 400, "invalid_input", "timeout_seconds requires after_revision")
			return
		}
		v, e = s.Query(r.Context(), p, id)
	}
	if e != nil {
		objectError(w, e)
		return
	}
	respond(w, 200, v)
}
