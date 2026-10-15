package delivery

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/workspace"
)

type fixture struct {
	db              *sql.DB
	w               *workspace.Workspace
	s               *Service
	p, dbpath, root string
}

func setup(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{root: t.TempDir(), dbpath: filepath.Join(t.TempDir(), "db.sqlite")}
	var e error
	f.db, e = store.Open(f.dbpath)
	if e != nil {
		t.Fatal(e)
	}
	f.w, e = workspace.Open(f.root)
	if e != nil {
		t.Fatal(e)
	}
	p, e := (store.Objects{DB: f.db}).CreateProject(context.Background(), "project", "Test")
	if e != nil {
		t.Fatal(e)
	}
	f.p = p.ID
	f.s = NewService(f.db, f.w, "user")
	t.Cleanup(func() { f.db.Close(); f.w.Close() })
	return f
}
func (f *fixture) input(t *testing.T, key string) Request {
	t.Helper()
	v := "projects/" + f.p + "/files/source.txt"
	if e := os.MkdirAll(filepath.Join(f.root, "projects", f.p, "files"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(f.root, filepath.FromSlash(v)), []byte("source content"), 0600); e != nil {
		t.Fatal(e)
	}
	return Request{RequestID: key, Sources: []Source{{Key: "one", Path: v}}, Output: Output{Resources: []Resource{{Key: "one", Mode: "create", Name: "resource"}}, Composition: Composition{Name: "Movie", Type: CompositionType{Label: "video"}, Placements: []Placement{{Key: "one", Role: "main"}}}}}
}
func count(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if e := db.QueryRow(q, args...).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}
func TestReplay(t *testing.T) {
	for _, point := range []string{"prepare", "archive", "before_commit", "after_commit"} {
		t.Run(point, func(t *testing.T) {
			f := setup(t)
			in := f.input(t, "deliver")
			f.s.fault = func(at string) error {
				if at == point {
					return errors.New("crash")
				}
				return nil
			}
			r, e := f.s.Deliver(context.Background(), f.p, in)
			if e != nil || r.Delivery.Status != "unknown" {
				t.Fatalf("fault %+v %v", r, e)
			}
			var id, archive string
			if e = f.db.QueryRow("SELECT version_id,archive_path FROM delivery_sources").Scan(&id, &archive); e != nil {
				t.Fatal(e)
			}
			f.db.Close()
			f.db, e = store.Open(f.dbpath)
			if e != nil {
				t.Fatal(e)
			}
			f.s = NewService(f.db, f.w, "agent")
			r, e = f.s.Deliver(context.Background(), f.p, in)
			if e != nil || r.Delivery.Status != "completed" || r.Delivery.Versions[0].VersionID != id {
				t.Fatalf("replay %+v %v", r, e)
			}
			again, e := f.s.Recover(context.Background(), f.p, in.RequestID)
			if e != nil || !reflect.DeepEqual(r, again) {
				t.Fatalf("recover %+v %v", again, e)
			}
			if count(t, f.db, "SELECT count(*) FROM versions WHERE resource_id=?", r.Delivery.Versions[0].ResourceID) != 1 || count(t, f.db, "SELECT count(*) FROM composition_members WHERE resource_id=?", r.Delivery.Versions[0].ResourceID) != 1 {
				t.Fatal("duplicate objects")
			}
			if count(t, f.db, "SELECT count(*) FROM versions WHERE origin='user'") != 1 {
				t.Fatal("origin changed")
			}
			if _, e = os.Stat(filepath.Join(f.root, filepath.FromSlash(archive))); e != nil {
				t.Fatal(e)
			}
			in.Output.Resources[0].Name = "different"
			if _, e = f.s.Deliver(context.Background(), f.p, in); !errors.Is(e, store.ErrRequestConflict) {
				t.Fatalf("conflict %v", e)
			}
		})
	}
}
func TestConcurrent(t *testing.T) {
	f := setup(t)
	in := f.input(t, "parallel")
	agent := NewService(f.db, f.w, "agent")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := f.s
			if i%2 == 1 {
				s = agent
			}
			r, e := s.Deliver(context.Background(), f.p, in)
			if e != nil || r.Delivery.Status != "completed" {
				t.Errorf("%+v %v", r, e)
			}
		}(i)
	}
	wg.Wait()
	if count(t, f.db, "SELECT count(*) FROM versions") != 1 {
		t.Fatal("duplicates")
	}
}
func TestBlocked(t *testing.T) {
	for _, kind := range []string{"missing-key", "duplicate-key", "missing-source", "task-output", "cross-project", "missing-base", "create-base", "placement"} {
		t.Run(kind, func(t *testing.T) {
			f := setup(t)
			in := f.input(t, "bad")
			switch kind {
			case "missing-key":
				in.Sources[0].Key = ""
			case "duplicate-key":
				in.Sources = append(in.Sources, in.Sources[0])
			case "missing-source":
				in.Sources[0].Path += "missing"
			case "task-output":
				in.Sources[0].Path = ""
				in.Sources[0].TaskOutputKey = "foreign-task-output"
			case "cross-project":
				in.Sources[0].Path = "projects/other/files/file.txt"
			case "missing-base":
				in.Output.Resources[0].Mode = "new_version"
				in.Output.Resources[0].BaseVersionID = "missing"
			case "create-base":
				in.Output.Resources[0].BaseVersionID = "invalid"
			case "placement":
				in.Output.Composition.Placements[0].Key = "absent"
			}
			r, e := f.s.Deliver(context.Background(), f.p, in)
			if e != nil || r.Delivery.Status != "blocked" {
				t.Fatalf("%+v %v", r, e)
			}
			r2, e := f.s.Deliver(context.Background(), f.p, in)
			if e != nil || !reflect.DeepEqual(r, r2) {
				t.Fatalf("blocked replay %+v %v", r2, e)
			}
			if count(t, f.db, "SELECT count(*) FROM versions") != 0 {
				t.Fatal("partial SQL")
			}
		})
	}
}

func TestBatchLineageAndReferences(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	in := f.input(t, "first")
	// Same canonical path is legal for independent keys: one Asset, two Resources.
	in.Sources = append(in.Sources, Source{Key: "two", Path: in.Sources[0].Path})
	in.Output.Resources = append(in.Output.Resources, Resource{Key: "two", Mode: "create", Name: "second"})
	in.Output.Composition.Placements = append(in.Output.Composition.Placements, Placement{Key: "two", Role: "support"})
	r, e := f.s.Deliver(ctx, f.p, in)
	if e != nil || r.Delivery.Status != "completed" {
		t.Fatalf("%+v %v", r, e)
	}
	if count(t, f.db, "SELECT count(*) FROM assets") != 1 || count(t, f.db, "SELECT count(*) FROM resources") != 2 {
		t.Fatal("repeated path identity")
	}
	first := r.Delivery.Versions[0]
	var archive string
	if e = f.db.QueryRow("SELECT path FROM assets WHERE id=?", first.AssetID).Scan(&archive); e != nil {
		t.Fatal(e)
	}
	next := Request{RequestID: "next", Sources: []Source{{Key: "one", Path: archive}}, Output: Output{Resources: []Resource{{Key: "one", Mode: "new_version", Name: "ignored", BaseVersionID: first.VersionID}}, Composition: in.Output.Composition}}
	next.Output.Composition.Placements = []Placement{{Key: "one", Role: "main"}}
	for _, key := range []string{"next", "branch"} {
		next.RequestID = key
		v, e := f.s.Deliver(ctx, f.p, next)
		if e != nil || v.Delivery.Status != "completed" || v.Delivery.Versions[0].ResourceID != first.ResourceID {
			t.Fatalf("lineage %+v %v", v, e)
		}
		if _, e = f.s.Deliver(ctx, f.p, next); e != nil {
			t.Fatal(e)
		}
	}
	if count(t, f.db, "SELECT count(*) FROM versions WHERE resource_id=?", first.ResourceID) != 3 || count(t, f.db, "SELECT count(*) FROM versions WHERE source_version_id=?", first.VersionID) != 2 || count(t, f.db, "SELECT count(*) FROM composition_members WHERE resource_id=?", first.ResourceID) != 1 {
		t.Fatal("lineage or placements duplicated")
	}
	var name string
	f.db.QueryRow("SELECT description FROM resources WHERE id=?", first.ResourceID).Scan(&name)
	if name != "resource" {
		t.Fatal("description overwritten")
	}
	for kind, id := range map[string]string{"versions": r.Delivery.Versions[1].VersionID, "resources": first.ResourceID, "assets": first.AssetID} {
		if _, e = (store.Objects{DB: f.db}).Mutate(ctx, f.p, kind, store.Mutation{RequestID: "delete-" + kind, Action: "delete", ID: id}); e == nil {
			t.Fatalf("deleted delivery %s", kind)
		}
	}
	next.RequestID = "incompatible"
	next.Output.Composition.Name = "Other"
	v, e := f.s.Deliver(ctx, f.p, next)
	if e != nil || v.Delivery.Status != "blocked" {
		t.Fatalf("incompatible %+v %v", v, e)
	}
}
func TestBatchAtomic(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	in := f.input(t, "batch")
	second := in.Sources[0].Path + "2.txt"
	if e := os.WriteFile(filepath.Join(f.root, filepath.FromSlash(second)), []byte("second"), 0600); e != nil {
		t.Fatal(e)
	}
	in.Sources = append(in.Sources, Source{Key: "two", Path: second})
	in.Output.Resources = append(in.Output.Resources, Resource{Key: "two", Mode: "create", Name: "second"})
	_, e := f.db.Exec(`CREATE TRIGGER reject_second BEFORE INSERT ON resources WHEN NEW.description='second' BEGIN SELECT RAISE(ABORT,'test failure'); END`)
	if e != nil {
		t.Fatal(e)
	}
	r, e := f.s.Deliver(ctx, f.p, in)
	if e != nil || r.Delivery.Status != "unknown" {
		t.Fatalf("%+v %v", r, e)
	}
	for _, table := range []string{"assets", "resources", "versions", "compositions", "composition_members", "composition_types"} {
		if count(t, f.db, "SELECT count(*) FROM "+table) != 0 {
			t.Fatalf("partial %s", table)
		}
	}
	f.db.Exec("DROP TRIGGER reject_second")
	r, e = f.s.Recover(ctx, f.p, in.RequestID)
	if e != nil || r.Delivery.Status != "completed" || count(t, f.db, "SELECT count(*) FROM versions") != 2 {
		t.Fatalf("%+v %v", r, e)
	}
}
func TestPendingReferencesBothDirections(t *testing.T) {
	for _, direction := range []string{"delivery-first", "file-first"} {
		t.Run(direction, func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			in := f.input(t, "pending")
			files := workspace.NewService(f.db, f.w)
			if direction == "delivery-first" {
				f.s.fault = func(at string) error {
					if at == "prepare" {
						return errors.New("crash")
					}
					return nil
				}
				r, e := f.s.Deliver(ctx, f.p, in)
				if e != nil || r.Delivery.Status != "unknown" {
					t.Fatal(r, e)
				}
				for _, op := range []string{"rename", "delete", "content"} {
					arg := workspace.Input{RequestID: op, Path: in.Sources[0].Path, Paths: []string{in.Sources[0].Path}, NewName: "changed.txt", Content: "changed"}
					if _, e = files.Mutate(ctx, f.p, op, arg); !errors.Is(e, store.ErrRequestPending) {
						t.Fatalf("%s %v", op, e)
					}
				}
				var archive string
				f.db.QueryRow("SELECT archive_path FROM delivery_sources").Scan(&archive)
				if _, e = files.Mutate(ctx, f.p, "content", workspace.Input{RequestID: "archive-write", Path: archive, Content: "foreign"}); !errors.Is(e, store.ErrRequestPending) {
					t.Fatalf("target reservation %v", e)
				}
				f.s.fault = nil
				r, e = f.s.Recover(ctx, f.p, in.RequestID)
				if e != nil || r.Delivery.Status != "completed" {
					t.Fatal(r, e)
				}
			} else {
				raw, _ := json.Marshal([]map[string]string{{"source": in.Sources[0].Path, "target": in.Sources[0].Path + "new"}})
				_, e := f.db.Exec("INSERT INTO file_operations(project_id,request_id,operation,input_json,stage,source) VALUES(?,'file','rename','{}','prepared',?)", f.p, string(raw))
				if e != nil {
					t.Fatal(e)
				}
				r, e := f.s.Deliver(ctx, f.p, in)
				if e != nil || r.Delivery.Status != "unknown" || count(t, f.db, "SELECT count(*) FROM delivery_sources") != 0 {
					t.Fatal(r, e)
				}
				f.db.Exec("DELETE FROM file_operations WHERE request_id='file'")
				r, e = f.s.Deliver(ctx, f.p, in)
				if e != nil || r.Delivery.Status != "completed" {
					t.Fatal(r, e)
				}
			}
		})
	}
}
func TestAmbiguousAndCrossProjectBase(t *testing.T) {
	f := setup(t)
	in := f.input(t, "initial")
	r, e := f.s.Deliver(context.Background(), f.p, in)
	if e != nil {
		t.Fatal(e)
	}
	var cid, tid string
	f.db.QueryRow("SELECT id,type_id FROM compositions").Scan(&cid, &tid)
	f.db.Exec("INSERT INTO compositions(id,project_id,type_id,name) VALUES('duplicate',?,?,'Movie')", f.p, tid)
	in.RequestID = "ambiguous"
	in.Sources[0].Path = "projects/" + f.p + "/files/source.txt"
	os.WriteFile(filepath.Join(f.root, filepath.FromSlash(in.Sources[0].Path)), []byte("new"), 0600)
	out, e := f.s.Deliver(context.Background(), f.p, in)
	if e != nil || out.Delivery.Status != "blocked" {
		t.Fatal(out, e)
	}
	p, e := (store.Objects{DB: f.db}).CreateProject(context.Background(), "other-project", "Other")
	if e != nil {
		t.Fatal(e)
	}
	old := f.p
	f.p = p.ID
	cross := f.input(t, "cross-base")
	f.p = old
	cross.Output.Resources[0].Mode = "new_version"
	cross.Output.Resources[0].BaseVersionID = r.Delivery.Versions[0].VersionID
	out, e = f.s.Deliver(context.Background(), p.ID, cross)
	if e != nil || out.Delivery.Status != "blocked" {
		t.Fatal(out, e)
	}
}
func TestFrozenInput(t *testing.T) {
	f := setup(t)
	in := f.input(t, "raw")
	raw, _ := json.Marshal(in)
	decoded, e := store.DecodeValue[Request](raw)
	if e != nil {
		t.Fatal(e)
	}
	decoded.Output.Resources[0].Name = "mutated"
	if _, e = f.s.Deliver(context.Background(), f.p, decoded); e == nil {
		t.Fatal("executed input different from frozen wire")
	}
	r, e := f.s.Deliver(context.Background(), f.p, in)
	if e != nil || r.Delivery.Status != "completed" {
		t.Fatal(r, e)
	}
}

func TestChangedArchiveIsBlocked(t *testing.T) {
	f := setup(t)
	in := f.input(t, "changed")
	f.s.fault = func(at string) error {
		if at == "archive" {
			return errors.New("crash")
		}
		return nil
	}
	r, e := f.s.Deliver(context.Background(), f.p, in)
	if e != nil || r.Delivery.Status != "unknown" {
		t.Fatal(r, e)
	}
	var v string
	f.db.QueryRow("SELECT archive_path FROM delivery_sources").Scan(&v)
	if e = os.WriteFile(filepath.Join(f.root, filepath.FromSlash(v)), []byte("tampered"), 0600); e != nil {
		t.Fatal(e)
	}
	f.s.fault = nil
	r, e = f.s.Recover(context.Background(), f.p, in.RequestID)
	if e != nil || r.Delivery.Status != "blocked" || count(t, f.db, "SELECT count(*) FROM versions") != 0 {
		t.Fatal(r, e)
	}
	b, e := os.ReadFile(filepath.Join(f.root, filepath.FromSlash(v)))
	if e != nil || string(b) != "tampered" {
		t.Fatal("overwrote foreign content")
	}
}
func TestPendingRegisteredAsset(t *testing.T) {
	f := setup(t)
	in := f.input(t, "pending-asset")
	ctx := context.Background()
	files := workspace.NewService(f.db, f.w)
	a, e := files.Register(ctx, f.p, "register", in.Sources[0].Path)
	if e != nil {
		t.Fatal(e)
	}
	f.s.fault = func(at string) error {
		if at == "prepare" {
			return errors.New("crash")
		}
		return nil
	}
	r, e := f.s.Deliver(ctx, f.p, in)
	if e != nil || r.Delivery.Status != "unknown" {
		t.Fatal(r, e)
	}
	for _, action := range []string{"update", "delete"} {
		m := store.Mutation{RequestID: action, Action: action, ID: a.ID}
		if action == "update" {
			m.Value = json.RawMessage(`{"name":"changed"}`)
		}
		if _, e = (store.Objects{DB: f.db}).Mutate(ctx, f.p, "assets", m); e == nil {
			t.Fatalf("pending asset %s accepted", action)
		}
	}
}
func TestTargetChangedDuringRecovery(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	in := f.input(t, "target-change")
	f.s.fault = func(at string) error {
		if at == "prepare" {
			return errors.New("crash")
		}
		return nil
	}
	r, e := f.s.Deliver(ctx, f.p, in)
	if e != nil || r.Delivery.Status != "unknown" {
		t.Fatal(r, e)
	}
	_, e = (store.Objects{DB: f.db}).Mutate(ctx, f.p, "composition_types", store.Mutation{RequestID: "competing", Action: "create", Value: json.RawMessage(`{"label":"video"}`)})
	if e != nil {
		t.Fatal(e)
	}
	f.s.fault = nil
	r, e = f.s.Recover(ctx, f.p, in.RequestID)
	if e != nil || r.Delivery.Status != "blocked" {
		t.Fatalf("permanent target conflict %+v %v", r, e)
	}
}
func TestWireOptionals(t *testing.T) {
	f := setup(t)
	in := f.input(t, "optionals")
	raw, _ := json.Marshal(in)
	for _, bad := range []string{strings.Replace(string(raw), `"mode":"create"`, `"mode":"create","baseVersionId":null`, 1), strings.Replace(string(raw), `"label":"video"`, `"label":"video","id":""`, 1), strings.Replace(string(raw), `"path":`, `"taskOutputKey":null,"path":`, 1)} {
		if _, e := store.DecodeValue[Request]([]byte(bad)); e == nil {
			t.Fatalf("accepted invalid optional %s", bad)
		}
	}
}

func TestTypeIDAndPendingBase(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	in := f.input(t, "first")
	r, e := f.s.Deliver(ctx, f.p, in)
	if e != nil || r.Delivery.Status != "completed" {
		t.Fatal(r, e)
	}
	v := r.Delivery.Versions[0]
	var tid, archive string
	f.db.QueryRow("SELECT type_id FROM compositions").Scan(&tid)
	f.db.QueryRow("SELECT path FROM assets WHERE id=?", v.AssetID).Scan(&archive)
	// Use a CRUD-created leaf base to prove pending delivery adds deletion protection.
	raw, e := (store.Objects{DB: f.db}).Mutate(ctx, f.p, "versions", store.Mutation{RequestID: "leaf", Action: "create", Value: json.RawMessage(`{"resource_id":"` + v.ResourceID + `","asset_id":"` + v.AssetID + `"}`)})
	if e != nil {
		t.Fatal(e)
	}
	var leaf struct {
		ID string `json:"id"`
	}
	json.Unmarshal(raw, &leaf)
	in.RequestID = "id-only"
	in.Sources[0].Path = archive
	in.Output.Composition.Type = CompositionType{ID: tid}
	in.Output.Resources[0].Mode = "new_version"
	in.Output.Resources[0].BaseVersionID = leaf.ID
	f.s.fault = func(at string) error {
		if at == "prepare" {
			return errors.New("crash")
		}
		return nil
	}
	r, e = f.s.Deliver(ctx, f.p, in)
	if e != nil || r.Delivery.Status != "unknown" {
		t.Fatal(r, e)
	}
	if _, e = (store.Objects{DB: f.db}).Mutate(ctx, f.p, "versions", store.Mutation{RequestID: "remove-leaf", Action: "delete", ID: leaf.ID}); e == nil {
		t.Fatal("pending base deleted")
	}
	f.s.fault = nil
	r, e = f.s.Recover(ctx, f.p, in.RequestID)
	if e != nil || r.Delivery.Status != "completed" {
		t.Fatal(r, e)
	}
	in.RequestID = "mismatch-label"
	in.Output.Composition.Type.Label = "wrong"
	r, e = f.s.Deliver(ctx, f.p, in)
	if e != nil || r.Delivery.Status != "blocked" {
		t.Fatal(r, e)
	}
}

func TestPendingSourceGapRecovery(t *testing.T) {
	for _, afterCheck := range []bool{false, true} {
		t.Run(fmt.Sprintf("after_early_check_%v", afterCheck), func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			in := f.input(t, "source-gap")
			source := in.Sources[0].Path
			disk := filepath.Join(f.root, filepath.FromSlash(source))
			away := disk + ".away"
			prepareGap := func() {
				t.Helper()
				raw, _ := json.Marshal([]map[string]string{{"source": source, "target": source + ".away"}})
				if _, e := f.db.Exec("INSERT INTO file_operations(project_id,request_id,operation,input_json,stage,source) VALUES(?,'gap','content','{}','prepared',?)", f.p, string(raw)); e != nil {
					t.Fatal(e)
				}
				if e := os.Rename(disk, away); e != nil {
					t.Fatal(e)
				}
			}
			if afterCheck {
				f.s.fault = func(point string) error {
					if point == "before_inspect" {
						prepareGap()
					}
					return nil
				}
			} else {
				prepareGap()
			}
			r, e := f.s.Deliver(ctx, f.p, in)
			if e != nil || r.Delivery.Status != "unknown" {
				t.Fatalf("gap must be recoverable: %+v %v", r, e)
			}
			if count(t, f.db, "SELECT count(*) FROM delivery_sources") != 0 {
				t.Fatal("source gap allocated a plan")
			}
			if e = os.Rename(away, disk); e != nil {
				t.Fatal(e)
			}
			if _, e = f.db.Exec("UPDATE file_operations SET stage='completed' WHERE request_id='gap'"); e != nil {
				t.Fatal(e)
			}
			f.s.fault = nil
			r, e = f.s.Deliver(ctx, f.p, in)
			if e != nil || r.Delivery.Status != "completed" || count(t, f.db, "SELECT count(*) FROM versions") != 1 {
				t.Fatalf("same key recovery: %+v %v", r, e)
			}
		})
	}
}
func TestPendingTargetRenameProtection(t *testing.T) {
	for _, kind := range []string{"compositions", "composition_types"} {
		t.Run(kind, func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			in := f.input(t, "first")
			first, e := f.s.Deliver(ctx, f.p, in)
			if e != nil || first.Delivery.Status != "completed" {
				t.Fatal(first, e)
			}
			var target, source string
			f.db.QueryRow("SELECT id FROM " + kind + " LIMIT 1").Scan(&target)
			f.db.QueryRow("SELECT path FROM assets LIMIT 1").Scan(&source)
			pending := in
			pending.RequestID = "pending-target"
			pending.Sources = []Source{{Key: "one", Path: source}}
			f.s.fault = func(point string) error {
				if point == "prepare" {
					return errors.New("crash")
				}
				return nil
			}
			r, e := f.s.Deliver(ctx, f.p, pending)
			if e != nil || r.Delivery.Status != "unknown" {
				t.Fatal(r, e)
			}
			value := json.RawMessage(`{"name":"renamed"}`)
			if kind == "composition_types" {
				value = json.RawMessage(`{"label":"renamed"}`)
			}
			mutation := store.Mutation{RequestID: "rename-pending", Action: "update", ID: target, Value: value}
			unchanged := json.RawMessage(`{"name":"Movie"}`)
			if kind == "composition_types" {
				unchanged = json.RawMessage(`{"label":"video"}`)
			}
			if _, e = (store.Objects{DB: f.db}).Mutate(ctx, f.p, kind, store.Mutation{RequestID: "same-target-name", Action: "update", ID: target, Value: unchanged}); e != nil {
				t.Fatalf("unchanged target rejected: %v", e)
			}
			if kind == "compositions" {
				if _, e = (store.Objects{DB: f.db}).Mutate(ctx, f.p, kind, store.Mutation{RequestID: "attributes-pending", Action: "update", ID: target, Value: json.RawMessage(`{"attributes":"{}"}`)}); e != nil {
					t.Fatalf("unrelated attributes rejected: %v", e)
				}
			}
			if _, e = (store.Objects{DB: f.db}).Mutate(ctx, f.p, kind, mutation); !errors.Is(e, store.ErrRequestPending) {
				t.Fatalf("pending rename %v", e)
			}
			f.s.fault = nil
			r, e = f.s.Recover(ctx, f.p, pending.RequestID)
			if e != nil || r.Delivery.Status != "completed" {
				t.Fatal(r, e)
			}
			mutation.RequestID = "rename-completed"
			if _, e = (store.Objects{DB: f.db}).Mutate(ctx, f.p, kind, mutation); e != nil {
				t.Fatalf("completed target should be renamable: %v", e)
			}
			replay, e := f.s.Deliver(ctx, f.p, in)
			if e != nil || !reflect.DeepEqual(first, replay) {
				t.Fatalf("completed replay after rename: %+v %v", replay, e)
			}
		})
	}
}
func TestPlacementsRequired(t *testing.T) {
	f := setup(t)
	in := f.input(t, "placements")
	raw, _ := json.Marshal(in)
	for _, bad := range []string{strings.Replace(string(raw), `,"placements":[{"key":"one","role":"main"}]`, "", 1), strings.Replace(string(raw), `"placements":[{"key":"one","role":"main"}]`, `"placements":null`, 1)} {
		if _, e := store.DecodeValue[Request]([]byte(bad)); e == nil {
			t.Fatalf("decoder accepted missing/null placements: %s", bad)
		}
	}
	in.Output.Composition.Placements = nil
	r, e := f.s.Deliver(context.Background(), f.p, in)
	if e != nil || r.Delivery.Status != "blocked" {
		t.Fatalf("direct nil placements: %+v %v", r, e)
	}
	for _, n := range []int{0, 50, 51} {
		in.RequestID = fmt.Sprintf("placement-count-%d", n)
		in.Output.Composition.Placements = make([]Placement, n)
		for i := range in.Output.Composition.Placements {
			in.Output.Composition.Placements[i] = Placement{Key: "one", Role: fmt.Sprintf("role-%d", i)}
		}
		wire, _ := json.Marshal(in)
		_, decodeError := store.DecodeValue[Request](wire)
		if (decodeError == nil) != (n == 50) {
			t.Fatalf("decoder count %d: %v", n, decodeError)
		}
		r, e = f.s.Deliver(context.Background(), f.p, in)
		want := "blocked"
		if n == 50 {
			want = "completed"
		}
		if e != nil || r.Delivery.Status != want {
			t.Fatalf("direct count %d: %+v %v", n, r, e)
		}
	}
}
