package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/workspace"
	"reflect"
	"strings"
	"testing"
)

func TestDirectCandidateDeliveryCanvasRestartReplay(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	first, e := f.s.Deliver(ctx, f.p, f.input(t, "base"))
	if e != nil || first.Delivery.Status != "completed" {
		t.Fatal(first, e)
	}
	remove := func(key string) {
		c := store.Canvas{DB: f.db}
		d, e := c.Document(ctx, f.p)
		if e != nil {
			t.Fatal(e)
		}
		changes := []any{}
		for id, n := range d.Nodes {
			changes = append(changes, map[string]any{"id": id, "before": n, "after": nil})
		}
		raw, _ := json.Marshal(map[string]any{"requestId": key, "expectedRevision": d.Revision, "command": map[string]any{"type": "canvas.patch", "nodes": changes, "edges": []any{}}})
		if _, e = c.Execute(ctx, f.p, raw); e != nil {
			t.Fatal(e)
		}
	}
	remove("remove-base")
	files := workspace.NewService(f.db, f.w)
	in := Request{RequestID: "candidates", Output: Output{Composition: f.input(t, "unused").Output.Composition}}
	in.Output.Composition.Placements = nil
	for _, key := range []string{"a", "b"} {
		path, e := files.Stage(ctx, f.p, "stage-"+key, key+".txt", strings.NewReader(key))
		if e != nil {
			t.Fatal(e)
		}
		in.Sources = append(in.Sources, Source{Key: key, Path: path})
		in.Output.Resources = append(in.Output.Resources, Resource{Key: key, Mode: "new_version", BaseVersionID: first.Delivery.Versions[0].VersionID, Name: key})
		in.Output.Composition.Placements = append(in.Output.Composition.Placements, Placement{Key: key, Role: "candidate"})
	}
	f.s.fault = func(at string) error {
		if at == "before_commit" {
			return errors.New("interrupted after archive")
		}
		return nil
	}
	unknown, e := f.s.Deliver(ctx, f.p, in)
	if e != nil || unknown.Delivery.Status != "unknown" {
		t.Fatal(unknown, e)
	}
	f.db.Close()
	f.db, e = store.Open(f.dbpath)
	if e != nil {
		t.Fatal(e)
	}
	f.s = NewService(f.db, f.w, "agent")
	result, e := f.s.Deliver(ctx, f.p, in)
	if e != nil || result.Delivery.Status != "completed" {
		t.Fatal(result, e)
	}
	d, e := (store.Canvas{DB: f.db}).Document(ctx, f.p)
	if e != nil || len(d.Nodes) != 2 {
		t.Fatal(d, e)
	}
	for _, v := range result.Delivery.Versions {
		n := d.Nodes["delivery-"+v.VersionID]
		if n == nil || n["activeItem"].(map[string]any)["versionId"] != v.VersionID || v.ResourceID != first.Delivery.Versions[0].ResourceID {
			t.Fatal("candidate identity lost", v, n)
		}
	}
	replay, e := f.s.Deliver(ctx, f.p, in)
	if e != nil || !reflect.DeepEqual(result, replay) {
		t.Fatal(replay, e)
	}
	if count(t, f.db, "SELECT count(*) FROM versions WHERE origin='user'") != 3 {
		t.Fatal("duplicated versions or changed actor")
	}
	remove("remove-results")
	f.db.Close()
	f.db, e = store.Open(f.dbpath)
	if e != nil {
		t.Fatal(e)
	}
	f.s = NewService(f.db, f.w, "agent")
	replay, e = f.s.Deliver(ctx, f.p, in)
	if e != nil || !reflect.DeepEqual(result, replay) {
		t.Fatal(replay, e)
	}
	d, e = (store.Canvas{DB: f.db}).Document(ctx, f.p)
	if e != nil || len(d.Nodes) != 0 {
		t.Fatal("completed replay resurrected nodes", d, e)
	}
}
