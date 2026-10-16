package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
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
	for _, tc := range []struct {
		name, path string
		status     int
	}{
		{"events-compound", "/api/events?project_id=" + p.ID + "&project_id%20session_id=ignored", 400},
		{"tasks-compound", "/api/tasks/" + v.ID + "?project_id=" + p.ID + "&after_revision%20timeout_seconds=ignored", 400},
		{"events-unknown", "/api/events?project_id=" + p.ID + "&unknown=x", 400},
		{"tasks-unknown", "/api/tasks/" + v.ID + "?project_id=" + p.ID + "&unknown=x", 400},
		{"events-duplicate", "/api/events?project_id=" + p.ID + "&session_id=a&session_id=b", 400},
		{"tasks-duplicate", "/api/tasks/" + v.ID + "?project_id=" + p.ID + "&after_revision=1&after_revision=1", 400},
		{"tasks-noncanonical", "/api/tasks/" + v.ID + "?project_id=" + p.ID + "&after_revision=01", 400},
		{"tasks-noncanonical-timeout", "/api/tasks/" + v.ID + "?project_id=" + p.ID + "&after_revision=1&timeout_seconds=00", 400},
		{"events-valid-session", "/api/events?project_id=" + p.ID + "&session_id=valid-session", 200},
		{"tasks-valid-options", "/api/tasks/" + v.ID + "?project_id=" + p.ID + "&after_revision=1&timeout_seconds=0", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := request(tc.path, "")
			r.Body.Close()
			if r.StatusCode != tc.status {
				t.Errorf("status %d want %d", r.StatusCode, tc.status)
			}
		})
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

func TestCollectionCancellationHealthAfterReopen(t *testing.T) {
	for _, phase := range []string{"queued", "active", "active-error", "interrupted"} {
		t.Run(phase, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cancel.db")
			db, err := store.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { db.Close() }()
			ctx := context.Background()
			p, err := (store.Objects{DB: db}).CreateProject(ctx, "p", "P")
			if err != nil {
				t.Fatal(err)
			}
			s := newHandler("http://tauri.localhost", db, nil).(*Server)
			defer func() { s.Close() }()
			in := tasks.Input{RequestID: "new", CapabilityID: "image", Prompt: "literal", Params: json.RawMessage(`{}`), Provider: providers.FrozenProvider{Config: providers.Config{ID: "provider", Enabled: true}}, Output: delivery.Output{Resources: []delivery.Resource{{Key: "one", Mode: "create", Name: "One"}}, Composition: delivery.Composition{Name: "Shot", Type: delivery.CompositionType{Label: "Shots"}, Placements: []delivery.Placement{{Key: "one", Role: "image"}}}}}
			v, err := s.tasks.Submit(ctx, p.ID, in)
			if err != nil {
				t.Fatal(err)
			}
			posts, collects, queries := 0, 0, 0
			entered, release := make(chan struct{}), make(chan struct{})
			executor := &tasks.Executor{Submit: func(_ context.Context, _ tasks.Attempt, checkpoint func(string) error) (tasks.Outcome, error) {
				posts++
				if err := checkpoint("remote-result"); err != nil {
					return tasks.Outcome{}, err
				}
				return tasks.Outcome{Status: tasks.Failed, Problem: &tasks.Problem{Source: "delivery", Code: "SAVE_FAILED", Retryable: true}, Result: []byte(`{"saved":true}`)}, nil
			}, Collect: func(context.Context, tasks.Attempt) (tasks.Outcome, error) {
				collects++
				close(entered)
				<-release
				if phase == "active-error" {
					return tasks.Outcome{}, errors.New("collector interrupted")
				}
				return tasks.Outcome{Status: tasks.Failed, Problem: &tasks.Problem{Source: "delivery", Code: "LATE_FAILURE", Retryable: true}, Result: []byte(`{"late":true}`)}, nil
			}, Query: func(context.Context, tasks.Attempt) (tasks.Outcome, error) { queries++; return tasks.Outcome{}, nil }}
			s.tasks.Executor = executor
			if _, err = db.Exec("UPDATE task_attempts SET state='approved' WHERE task_id=?", v.ID); err != nil {
				t.Fatal(err)
			}
			if err = s.tasks.Run(ctx, p.ID, v.ID); err != nil {
				t.Fatal(err)
			}
			v, err = s.tasks.Query(ctx, p.ID, v.ID)
			if err != nil {
				t.Fatal(err)
			}
			v, err = s.tasks.Resolve(ctx, p.ID, v.ID, tasks.ResolveInput{RequestID: "retry", Action: "retry", ExpectedRevision: v.Revision})
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			if strings.HasPrefix(phase, "active") {
				go func() { done <- s.tasks.Run(ctx, p.ID, v.ID) }()
				<-entered
				v, err = s.tasks.Query(ctx, p.ID, v.ID)
				if err != nil {
					t.Fatal(err)
				}
			} else if phase == "interrupted" {
				if _, err = db.Exec("UPDATE task_attempts SET state='collecting' WHERE task_id=?", v.ID); err != nil {
					t.Fatal(err)
				}
			}
			v, err = s.tasks.Resolve(ctx, p.ID, v.ID, tasks.ResolveInput{RequestID: "cancel", Action: "cancel", ExpectedRevision: v.Revision})
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(phase, "active") {
				if h := s.Health(); h.Idle || h.SafeToSuspend {
					t.Error("executing Collect omitted from health", h)
				}
				close(release)
				if err = <-done; err != nil {
					t.Fatal(err)
				}
			}
			if err = s.tasks.Run(ctx, p.ID, v.ID); err != nil {
				t.Fatal(err)
			}
			if phase != "interrupted" {
				if h := s.Health(); !h.Idle || !h.SafeToSuspend {
					t.Errorf("cancelled local collection stranded: %+v", h)
				}
			}
			s.Close()
			if err = db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err = store.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			s = newHandler("http://tauri.localhost", db, nil).(*Server)
			s.tasks.Executor = executor
			if err = s.tasks.Recover(ctx); err != nil {
				t.Fatal(err)
			}
			if h := s.Health(); !h.Idle || !h.SafeToSuspend {
				t.Error("reopen stranded cancelled collection", h)
			}
			v, err = s.tasks.Query(ctx, p.ID, v.ID)
			if err != nil || v.Status != tasks.Cancelled {
				t.Fatal("cancelled task revived", v, err)
			}
			var state, upstream, result, evidence string
			if err = db.QueryRow("SELECT state,upstream_id,result_json,error_json FROM task_attempts WHERE task_id=?", v.ID).Scan(&state, &upstream, &result, &evidence); err != nil {
				t.Fatal(err)
			}
			if state != "cancelled" && state != "failed" {
				t.Error("nonterminal collection attempt", state)
			}
			wantCollect := 0
			if strings.HasPrefix(phase, "active") {
				wantCollect = 1
			}
			if upstream != "remote-result" || result != `{"saved":true}` || !strings.Contains(evidence, "SAVE_FAILED") || posts != 1 || collects != wantCollect || queries != 0 {
				t.Fatal("lost evidence or repeated adapter call", upstream, result, evidence, posts, collects, queries)
			}
		})
	}
}
