package server

import (
	"encoding/json"
	"fmt"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/events"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"net/http"
	"strconv"
	"time"
)

func serveEvents(w http.ResponseWriter, r *http.Request, s *events.Service) {
	if r.Method != "GET" {
		reject(w, 405, "invalid_input", "Method not allowed")
		return
	}
	q, e := scopedQuery(r, "project_id session_id")
	if e != nil {
		reject(w, 400, "invalid_input", e.Error())
		return
	}
	session := q.Get("session_id")
	if q.Has("session_id") {
		if e = (store.Scope{ProjectID: "session", Operation: "events", RequestID: session}).Validate(); e != nil {
			reject(w, 400, "invalid_input", "Invalid session_id")
			return
		}
	}
	values := r.Header.Values("Last-Event-ID")
	cursor := int64(0)
	if len(values) > 1 {
		reject(w, 400, "invalid_input", "Repeated event cursor")
		return
	}
	if len(values) == 1 {
		cursor, e = strconv.ParseInt(values[0], 10, 64)
		if e != nil || cursor < 0 || strconv.FormatInt(cursor, 10) != values[0] {
			reject(w, 400, "invalid_input", "Invalid event cursor")
			return
		}
	}
	// Subscribe before the first database read. Notifications contain no payloads;
	// each connection holds at most one wake and one bounded 100-row replay page.
	ch, done := s.Subscribe()
	defer done()
	batch, e := s.Read(r.Context(), q.Get("project_id"), session, cursor)
	if e != nil {
		objectError(w, e)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	rc := http.NewResponseController(w)
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	defer rc.SetWriteDeadline(time.Time{})
	if e = rc.SetWriteDeadline(time.Now().Add(20 * time.Second)); e != nil && e != http.ErrNotSupported {
		return
	}
	if _, e = fmt.Fprint(w, ": connected\n\n"); e != nil {
		return
	}
	if e = rc.Flush(); e != nil {
		return
	}
	for {
		for _, ev := range batch {
			raw, err := json.Marshal(ev)
			if err != nil {
				return
			}
			_ = rc.SetWriteDeadline(time.Now().Add(20 * time.Second))
			if _, e = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", ev.ID, ev.Type, raw); e != nil {
				return
			}
			if e = rc.Flush(); e != nil {
				return
			}
			cursor = ev.ID
		}
		if len(batch) == 100 {
			batch, e = s.Read(r.Context(), q.Get("project_id"), session, cursor)
			if e != nil {
				return
			}
			continue
		}
		select {
		case <-r.Context().Done():
			return
		case _, open := <-ch:
			if !open {
				return
			}
		case <-heartbeat.C:
			_ = rc.SetWriteDeadline(time.Now().Add(20 * time.Second))
			if _, e = fmt.Fprint(w, ": heartbeat\n\n"); e != nil {
				return
			}
			if e = rc.Flush(); e != nil {
				return
			}
		}
		batch, e = s.Read(r.Context(), q.Get("project_id"), session, cursor)
		if e != nil {
			return
		}
	}
}
