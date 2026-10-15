package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/delivery"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/tasks"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTasksSSEReplayLiveHealthAndShutdown(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "http.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	p, e := (store.Objects{DB: db}).CreateProject(context.Background(), "p", "p")
	if e != nil {
		t.Fatal(e)
	}
	s := newHandler("http://tauri.localhost", db, nil).(*Server)
	defer s.Close()
	host := httptest.NewServer(s)
	defer host.Close()
	i := tasks.Input{RequestID: "new", CapabilityID: "image", Prompt: "literal", Params: json.RawMessage(`{}`), Provider: providers.FrozenProvider{Config: providers.Config{ID: "provider", Enabled: true}}, Output: delivery.Output{Resources: []delivery.Resource{{Key: "one", Mode: "create", Name: "One"}}, Composition: delivery.Composition{Name: "Shot", Type: delivery.CompositionType{Label: "Shots"}, Placements: []delivery.Placement{{Key: "one", Role: "image"}}}}}
	v, e := s.tasks.Submit(context.Background(), p.ID, i)
	if e != nil {
		t.Fatal(e)
	}
	if h := s.Health(); !h.Idle || !h.SafeToSuspend {
		t.Fatal("billing wait fakes active work", h)
	}
	request := func(path, cursor string) *http.Response {
		t.Helper()
		r, _ := http.NewRequest("GET", host.URL+path, nil)
		r.Header.Set("Origin", "http://tauri.localhost")
		if cursor != "" {
			r.Header.Set("Last-Event-ID", cursor)
		}
		response, e := host.Client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		return response
	}
	for _, cursor := range []string{"01", "-1", "+1", "999999"} {
		r := request("/api/events?project_id="+p.ID, cursor)
		r.Body.Close()
		if r.StatusCode != 400 {
			t.Fatal("invalid cursor", cursor, r.StatusCode)
		}
	}
	r := request("/api/events?project_id="+p.ID, "")
	defer r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatal(r.Status)
	}
	scanner := bufio.NewScanner(r.Body)
	readEvent := func() string {
		t.Helper()
		for scanner.Scan() {
			if strings.HasPrefix(scanner.Text(), "id: ") {
				return strings.TrimPrefix(scanner.Text(), "id: ")
			}
		}
		t.Fatal("stream ended", scanner.Err())
		return ""
	}
	first := readEvent()
	if first != "1" {
		t.Fatal(first)
	}
	if h := s.Health(); !h.Idle {
		t.Fatal("SSE counted as CPU work")
	}
	next, e := s.tasks.Resolve(context.Background(), p.ID, v.ID, tasks.ResolveInput{RequestID: "cancel", Action: "cancel", ExpectedRevision: 1})
	if e != nil {
		t.Fatal(e)
	}
	second := readEvent()
	if second == first {
		t.Fatal("live event duplicate")
	}
	r.Body.Close()
	replay := request("/api/events?project_id="+p.ID, first)
	scan := bufio.NewScanner(replay.Body)
	found := false
	for scan.Scan() {
		if scan.Text() == "id: "+second {
			found = true
			break
		}
	}
	replay.Body.Close()
	if !found {
		t.Fatal("cursor replay missing")
	}
	q := request(fmt.Sprintf("/api/tasks/%s?project_id=%s", v.ID, p.ID), "")
	var got tasks.TaskView
	if e = json.NewDecoder(q.Body).Decode(&got); e != nil {
		t.Fatal(e)
	}
	q.Body.Close()
	if got.Revision != next.Revision {
		t.Fatal(got)
	}
	r = request("/api/events?project_id="+p.ID, second)
	done := make(chan struct{})
	go func() {
		defer close(done)
		scan := bufio.NewScanner(r.Body)
		for scan.Scan() {
		}
		r.Body.Close()
	}()
	s.BeginShutdown()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stream did not close during shutdown")
	}
}
