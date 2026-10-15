package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/delivery"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/events"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWaitSemantics(t *testing.T) {
	for _, tc := range []struct {
		s       Status
		source  string
		settles bool
	}{{Accepted, "", false}, {Running, "", false}, {WaitingUser, "billing", false}, {WaitingUser, "validation", true}, {Unknown, "", true}, {Completed, "", true}, {Failed, "", true}, {Cancelled, "", true}} {
		if SettlesAgentWait(tc.s, tc.source) != tc.settles {
			t.Errorf("%s/%s settlement incorrect", tc.s, tc.source)
		}
	}
}

func setup(t *testing.T) (*Service, string, Input) {
	t.Helper()
	db, e := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	p, e := (store.Objects{DB: db}).CreateProject(context.Background(), "p", "create")
	if e != nil {
		t.Fatal(e)
	}
	s := NewService(db, events.NewService(db), nil)
	t.Cleanup(func() { s.Close(); s.Events.Close() })
	i := Input{RequestID: "submit", CapabilityID: "image", Prompt: " exact  prompt\n", Params: json.RawMessage(`{}`), References: []Reference{}, Provider: providers.FrozenProvider{Config: providers.Config{ID: "provider", Enabled: true}, CredentialVersion: "immutable-version"}, Output: delivery.Output{Resources: []delivery.Resource{{Key: "image", Mode: "create", Name: "Image"}}, Composition: delivery.Composition{Name: "Shot", Type: delivery.CompositionType{Label: "Shots"}, Placements: []delivery.Placement{{Key: "image", Role: "image"}}}}}
	return s, p.ID, i
}
func TestSubmissionReceiptCASAndWait(t *testing.T) {
	s, p, i := setup(t)
	ctx := context.Background()
	v, e := s.Submit(ctx, p, i)
	if e != nil {
		t.Fatal(e)
	}
	if v.Status != WaitingUser || v.Problem.Source != "billing" || v.Prompt != i.Prompt || len(v.Outputs) != 1 {
		t.Fatal(v)
	}
	var n int
	if e = s.DB.QueryRow("SELECT count(*) FROM events WHERE task_id=?", v.ID).Scan(&n); e != nil || n != 1 {
		t.Fatal("atomic event", n, e)
	}
	i.Provider.Config.Enabled = false
	replay, e := s.Submit(ctx, p, i)
	if e != nil || replay.ID != v.ID {
		t.Fatal("immutable replay", e)
	}
	i.Prompt += " "
	if _, e = s.Submit(ctx, p, i); !errors.Is(e, store.ErrRequestConflict) {
		t.Fatal("changed input reused key", e)
	}
	wait, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	if _, e = s.Wait(wait, p, v.ID); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal("billing wait did not penetrate", e)
	}
	if _, e = s.Resolve(ctx, p, v.ID, ResolveInput{RequestID: "stale", Action: "cancel", ExpectedRevision: 99}); e == nil {
		t.Fatal("stale CAS accepted")
	}
	out, e := s.Resolve(ctx, p, v.ID, ResolveInput{RequestID: "cancel", Action: "cancel", ExpectedRevision: 1})
	if e != nil || out.Status != Cancelled {
		t.Fatal(out, e)
	}
	again, e := s.Resolve(ctx, p, v.ID, ResolveInput{RequestID: "cancel", Action: "cancel", ExpectedRevision: 1})
	if e != nil || again.Revision != out.Revision {
		t.Fatal("resolve replay", e)
	}
	if _, e = s.Query(ctx, "other", v.ID); e == nil {
		t.Fatal("cross-project query")
	}
	if _, e = s.Wait(ctx, p, v.ID); e != nil {
		t.Fatal(e)
	}
}
func TestWorkersClaimOnceAndRecoverUnknown(t *testing.T) {
	s, p, i := setup(t)
	ctx := context.Background()
	v, e := s.Submit(ctx, p, i)
	if e != nil {
		t.Fatal(e)
	}
	var posts atomic.Int64
	s.Executor = &Executor{Submit: func(ctx context.Context, a Attempt, checkpoint func(string) error) (Outcome, error) {
		posts.Add(1)
		return Outcome{}, errors.New("connection lost after write")
	}}
	if e = s.Run(ctx, p, v.ID); e != nil {
		t.Fatal(e)
	}
	if posts.Load() != 0 {
		t.Fatal("unapproved execution")
	}
	if _, e = s.DB.Exec("UPDATE task_attempts SET state='approved' WHERE task_id=?", v.ID); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	for x := 0; x < 2; x++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = s.Run(ctx, p, v.ID) }()
	}
	wg.Wait()
	if posts.Load() != 1 {
		t.Fatalf("wanted one POST, got %d", posts.Load())
	}
	out, e := s.Query(ctx, p, v.ID)
	if e != nil || out.Status != Unknown {
		t.Fatal("transport uncertainty", out, e)
	}
	if _, e = s.Resolve(ctx, p, v.ID, ResolveInput{RequestID: "retry", Action: "retry", ExpectedRevision: out.Revision}); e == nil {
		t.Fatal("unknown rerun allowed")
	}
	if e = s.Recover(ctx); e != nil {
		t.Fatal(e)
	}
	if posts.Load() != 1 {
		t.Fatal("restart resubmitted")
	}
}

func TestRetryPreservesInputAndCollectionAttempt(t *testing.T) {
	for _, collection := range []bool{false, true} {
		t.Run(fmt.Sprint(collection), func(t *testing.T) {
			s, p, i := setup(t)
			ctx := context.Background()
			v, e := s.Submit(ctx, p, i)
			if e != nil {
				t.Fatal(e)
			}
			source := "provider"
			if collection {
				source = "delivery"
			}
			s.Executor = &Executor{Submit: func(context.Context, Attempt, func(string) error) (Outcome, error) {
				return Outcome{Status: Failed, Problem: &Problem{Source: source, Code: "RETRYABLE", Message: "Temporary failure", Retryable: true}, Result: []byte(`{"accepted":true}`)}, nil
			}}
			if _, e = s.DB.Exec("UPDATE task_attempts SET state='approved' WHERE task_id=?", v.ID); e != nil {
				t.Fatal(e)
			}
			if e = s.Run(ctx, p, v.ID); e != nil {
				t.Fatal(e)
			}
			v, e = s.Query(ctx, p, v.ID)
			if e != nil {
				t.Fatal(e)
			}
			v, e = s.Resolve(ctx, p, v.ID, ResolveInput{RequestID: "retry", Action: "retry", ExpectedRevision: v.Revision})
			if e != nil {
				t.Fatal(e)
			}
			var attempt int
			var frozen, provider, target string
			if e = s.DB.QueryRow("SELECT current_attempt,frozen_input_json,provider_json FROM tasks WHERE id=?", v.ID).Scan(&attempt, &frozen, &provider); e != nil {
				t.Fatal(e)
			}
			want := 2
			if collection {
				want = 1
			}
			if attempt != want {
				t.Fatalf("retry attempt %d want %d", attempt, want)
			}
			if !collection && v.Problem.Source != "billing" {
				t.Fatal("generation retry skipped billing")
			}
			if v.Prompt != i.Prompt || !strings.Contains(provider, "immutable-version") {
				t.Fatal("frozen intent changed")
			}
			if e = s.DB.QueryRow("SELECT source_json FROM task_outputs WHERE task_id=? AND attempt=?", v.ID, attempt).Scan(&target); e != nil || target == "" {
				t.Fatal("lost reserved target", e)
			}
		})
	}
}
func TestSubmitAtomicRollbackAndRawIdentity(t *testing.T) {
	s, p, i := setup(t)
	ctx := context.Background()
	if _, e := s.DB.Exec("CREATE TRIGGER reject_task_event BEFORE INSERT ON events BEGIN SELECT RAISE(ABORT,'event failed');END"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Submit(ctx, p, i); e == nil {
		t.Fatal("event failure accepted")
	}
	var n int
	s.DB.QueryRow("SELECT count(*) FROM tasks").Scan(&n)
	if n != 0 {
		t.Fatal("task committed without event")
	}
	s.DB.Exec("DROP TRIGGER reject_task_event")
	raw, _ := json.Marshal(i)
	var in Input
	if e := json.Unmarshal(raw, &in); e != nil {
		t.Fatal(e)
	}
	in.Provider = i.Provider
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	v, e := s.Submit(cancelled, p, in)
	if e != nil {
		t.Fatal("request disconnect cancelled durable work", e)
	}
	var fields map[string]any
	json.Unmarshal(raw, &fields)
	fields["session_id"] = nil
	changed, _ := json.Marshal(fields)
	json.Unmarshal(changed, &in)
	in.Provider = i.Provider
	if _, e = s.Submit(ctx, p, in); !errors.Is(e, store.ErrRequestConflict) {
		t.Fatal("missing/null original fingerprint collapsed", e)
	}
	if _, e = s.Query(ctx, p, v.ID); e != nil {
		t.Fatal(e)
	}
}

func TestRestartCheckpointAndLateCancellation(t *testing.T) {
	s, p, i := setup(t)
	ctx := context.Background()
	v, e := s.Submit(ctx, p, i)
	if e != nil {
		t.Fatal(e)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	var posts, queries atomic.Int64
	s.Executor = &Executor{Submit: func(ctx context.Context, a Attempt, checkpoint func(string) error) (Outcome, error) {
		posts.Add(1)
		if a.Provider.CredentialVersion != "immutable-version" {
			return Outcome{}, errors.New("snapshot changed")
		}
		if e := checkpoint("remote-123"); e != nil {
			return Outcome{}, e
		}
		close(entered)
		<-release
		return Outcome{}, errors.New("response lost")
	}, Query: func(ctx context.Context, a Attempt) (Outcome, error) {
		queries.Add(1)
		if a.UpstreamID != "remote-123" || a.Provider.CredentialVersion != "immutable-version" {
			return Outcome{}, errors.New("restart lost identity")
		}
		return Outcome{Status: Failed, Problem: &Problem{Source: "provider", Code: "FAILED", Message: "Definitive failure"}}, nil
	}}
	s.DB.Exec("UPDATE task_attempts SET state='approved' WHERE task_id=?", v.ID)
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx, p, v.ID) }()
	<-entered
	v, e = s.Query(ctx, p, v.ID)
	if e != nil {
		t.Fatal(e)
	}
	v, e = s.Resolve(ctx, p, v.ID, ResolveInput{RequestID: "cancel", Action: "cancel", ExpectedRevision: v.Revision})
	if e != nil {
		t.Fatal(e)
	}
	close(release)
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	restarted := NewService(s.DB, s.Events, s.Executor)
	defer restarted.Close()
	if e = restarted.Recover(ctx); e != nil {
		t.Fatal(e)
	}
	if posts.Load() != 1 || queries.Load() != 1 {
		t.Fatal("restart did not reconcile read-only", posts.Load(), queries.Load())
	}
	v, e = s.Query(ctx, p, v.ID)
	if e != nil || v.Status != Cancelled {
		t.Fatal("late result revived cancellation", v, e)
	}
	var state, upstream, originalError string
	if e = s.DB.QueryRow("SELECT state,upstream_id,error_json FROM task_attempts WHERE task_id=?", v.ID).Scan(&state, &upstream, &originalError); e != nil || state != "failed" || upstream != "remote-123" || !strings.Contains(originalError, "PROVIDER_OUTCOME_UNKNOWN") {
		t.Fatal("late reconciliation lost immutable evidence", state, upstream, originalError, e)
	}
}
func TestOriginalWireReplayAndTargetReservation(t *testing.T) {
	s, p, i := setup(t)
	ctx := context.Background()
	i.OriginalWire = json.RawMessage(`{"requestId":"submit","references":[],"prompt":"wire"}`)
	v, e := s.Submit(ctx, p, i)
	if e != nil {
		t.Fatal(e)
	}
	replayed, exists, e := s.ReplaySubmission(ctx, p, i.RequestID, i.OriginalWire)
	if e != nil || !exists || replayed.ID != v.ID {
		t.Fatal("wire replay", e)
	}
	if _, _, e = s.ReplaySubmission(ctx, p, i.RequestID, []byte(`{"requestId":"submit","references":null,"prompt":"wire"}`)); !errors.Is(e, store.ErrRequestConflict) {
		t.Fatal("wire null collapsed", e)
	}
	objects := store.Objects{DB: s.DB}
	if _, e = objects.Mutate(ctx, p, "composition_types", store.Mutation{RequestID: "collision", Action: "create", Value: json.RawMessage(`{"label":"Shots"}`)}); !errors.Is(e, store.ErrRequestPending) {
		t.Fatal("reserved target collision accepted", e)
	}
}

func TestDatabaseReopenRecoversStartedSubmission(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "restart.db")
	db, e := store.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	p, e := (store.Objects{DB: db}).CreateProject(ctx, "restart", "project")
	if e != nil {
		t.Fatal(e)
	}
	seed, _, i := setup(t)
	_ = seed
	s := NewService(db, events.NewService(db), nil)
	v, e := s.Submit(ctx, p.ID, i)
	if e != nil {
		t.Fatal(e)
	}
	v.Status = Running
	v.Problem = nil
	raw, _ := json.Marshal(v)
	if _, e = db.Exec("UPDATE tasks SET status='running',view_json=? WHERE id=?", string(raw), v.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("UPDATE task_attempts SET state='submitting',submit_started_at=1 WHERE task_id=?", v.ID); e != nil {
		t.Fatal(e)
	}
	s.Close()
	s.Events.Close()
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
	db, e = store.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	var posts atomic.Int64
	reopened := NewService(db, events.NewService(db), &Executor{Submit: func(context.Context, Attempt, func(string) error) (Outcome, error) {
		posts.Add(1)
		return Outcome{}, nil
	}})
	defer reopened.Close()
	defer reopened.Events.Close()
	if e = reopened.Recover(ctx); e != nil {
		t.Fatal(e)
	}
	v, e = reopened.Query(ctx, p.ID, v.ID)
	if e != nil || v.Status != Unknown || posts.Load() != 0 {
		t.Fatal("reopen resubmitted or lost unknown", v, e, posts.Load())
	}
	if _, e = reopened.Wait(ctx, p.ID, v.ID); e != nil {
		t.Fatal(e)
	}
}

func TestTaskFrozenInputProtectsAssetAndReservedIdentity(t *testing.T) {
	s, p, i := setup(t)
	ctx := context.Background()
	objects := store.Objects{DB: s.DB}
	mutate := func(kind, key, raw string) string {
		b, e := objects.Mutate(ctx, p, kind, store.Mutation{RequestID: key, Action: "create", Value: json.RawMessage(raw)})
		if e != nil {
			t.Fatal(e)
		}
		var v struct{ ID string }
		json.Unmarshal(b, &v)
		return v.ID
	}
	typ := mutate("composition_types", "type", `{"label":"Existing"}`)
	cid := mutate("compositions", "comp", `{"name":"Target","type_id":"`+typ+`"}`)
	_, e := s.DB.Exec("INSERT INTO assets(id,path,size,mtime_ms,name,created_at,updated_at)VALUES('asset',?,1,1,'file',1,1)", "projects/"+p+"/files/file.png")
	if e != nil {
		t.Fatal(e)
	}
	s.DB.Exec("INSERT INTO asset_projects(asset_id,project_id)VALUES('asset',?)", p)
	i.References = []Reference{{AssetID: "asset", Role: "reference"}}
	i.Output.Composition.Name = "Target"
	i.Output.Composition.Type = delivery.CompositionType{ID: typ}
	v, e := s.Submit(ctx, p, i)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = objects.Mutate(ctx, p, "assets", store.Mutation{RequestID: "delete", Action: "delete", ID: "asset"}); e == nil {
		t.Fatal("frozen input asset deletion allowed")
	}
	if _, e = objects.Mutate(ctx, p, "compositions", store.Mutation{RequestID: "rename", Action: "update", ID: cid, Value: json.RawMessage(`{"name":"Other"}`)}); !errors.Is(e, store.ErrRequestPending) {
		t.Fatal("bound composition renamed", e)
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = store.CheckTaskAssetPath(ctx, tx, p, "projects/"+p+"/files"); e == nil {
		t.Fatal("folder deletion bypasses frozen input")
	}
	tx.Rollback()
	var source string
	if e = s.DB.QueryRow("SELECT source_json FROM task_outputs WHERE task_id=?", v.ID).Scan(&source); e != nil || !strings.Contains(source, cid) {
		t.Fatal("target identity not frozen", e)
	}
}

func TestOutputReservationUsesResourceHistoryAndSurvivesRetry(t *testing.T) {
	s, p, i := setup(t)
	ctx := context.Background()
	objects := store.Objects{DB: s.DB}
	create := func(kind, key, raw string) string {
		b, e := objects.Mutate(ctx, p, kind, store.Mutation{RequestID: key, Action: "create", Value: json.RawMessage(raw)})
		if e != nil {
			t.Fatal(e)
		}
		var v struct{ ID string }
		json.Unmarshal(b, &v)
		return v.ID
	}
	typ := create("composition_types", "type", `{"label":"Existing"}`)
	cid := create("compositions", "comp", `{"name":"Target","type_id":"`+typ+`"}`)
	rid := create("resources", "resource", `{"composition_id":"`+cid+`"}`)
	base := create("versions", "version", `{"resource_id":"`+rid+`"}`)
	i.Output.Composition.Name = "Target"
	i.Output.Composition.Type = delivery.CompositionType{ID: typ}
	i.Output.Resources[0].Mode = "new_version"
	i.Output.Resources[0].BaseVersionID = base
	first, e := s.Submit(ctx, p, i)
	if e != nil {
		t.Fatal(e)
	}
	i.RequestID = "second"
	second, e := s.Submit(ctx, p, i)
	if e != nil {
		t.Fatal(e)
	}
	var firstSource, secondSource string
	s.DB.QueryRow("SELECT source_json FROM task_outputs WHERE task_id=?", first.ID).Scan(&firstSource)
	s.DB.QueryRow("SELECT source_json FROM task_outputs WHERE task_id=?", second.ID).Scan(&secondSource)
	var a, b map[string]any
	json.Unmarshal([]byte(firstSource), &a)
	json.Unmarshal([]byte(secondSource), &b)
	if a["ordinal"] != float64(2) || b["ordinal"] != float64(3) || a["version_id"] == b["version_id"] {
		t.Fatal("reservation order/identity", a, b)
	}
	direct := create("versions", "direct", `{"resource_id":"`+rid+`"}`)
	var ordinal int
	s.DB.QueryRow("SELECT ordinal FROM canvas_version_state WHERE version_id=?", direct).Scan(&ordinal)
	if ordinal != 4 {
		t.Fatal("direct version stole task ordinal", ordinal)
	}
	i.RequestID = "new-resources"
	i.Output.Resources = []delivery.Resource{{Key: "a", Name: "A", Mode: "create"}, {Key: "b", Name: "B", Mode: "create"}}
	i.Output.Composition.Placements = []delivery.Placement{{Key: "a", Role: "image"}, {Key: "b", Role: "image"}}
	fresh, e := s.Submit(ctx, p, i)
	if e != nil {
		t.Fatal(e)
	}
	var bad int
	s.DB.QueryRow("SELECT count(*) FROM task_outputs WHERE task_id=? AND json_extract(source_json,'$.ordinal')<>1", fresh.ID).Scan(&bad)
	if bad != 0 {
		t.Fatal("different new resources did not each reserve ordinal 1")
	}
	s.Executor = &Executor{Submit: func(context.Context, Attempt, func(string) error) (Outcome, error) {
		return Outcome{Status: Failed, Problem: &Problem{Source: "provider", Code: "RETRY", Retryable: true}}, nil
	}}
	s.DB.Exec("UPDATE task_attempts SET state='approved' WHERE task_id=?", first.ID)
	if e = s.Run(ctx, p, first.ID); e != nil {
		t.Fatal(e)
	}
	failed, e := s.Query(ctx, p, first.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Resolve(ctx, p, first.ID, ResolveInput{RequestID: "retry", Action: "retry", ExpectedRevision: failed.Revision}); e != nil {
		t.Fatal(e)
	}
	var retrySource string
	s.DB.QueryRow("SELECT source_json FROM task_outputs WHERE task_id=? AND attempt=2", first.ID).Scan(&retrySource)
	if retrySource != firstSource {
		t.Fatal("retry changed logical output identity")
	}
}
