package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

func objectFixture(t *testing.T) (Objects, string, string) {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "objects.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s := Objects{DB: db}
	p, err := s.CreateProject(context.Background(), "项目", "p")
	if err != nil {
		t.Fatal(err)
	}
	q, err := s.CreateProject(context.Background(), "另一个", "q")
	if err != nil {
		t.Fatal(err)
	}
	return s, p.ID, q.ID
}
func mutateObject(t *testing.T, s Objects, p, kind, key, action, id, value string) json.RawMessage {
	t.Helper()
	out, err := s.Mutate(context.Background(), p, kind, Mutation{RequestID: key, Action: action, ID: id, Value: json.RawMessage(value)})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func objectID(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var v struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &v); err != nil || v.ID == "" {
		t.Fatalf("id: %s %v", raw, err)
	}
	return v.ID
}

func TestObjectsIdentityReplayOwnershipAndProtection(t *testing.T) {
	ctx := context.Background()
	s, p, q := objectFixture(t)
	typ := objectID(t, mutateObject(t, s, p, "composition_types", "t", "create", "", `{"label":"视频"}`))
	if got := objectID(t, mutateObject(t, s, p, "composition_types", "t2", "create", "", `{"label":"视频"}`)); got != typ {
		t.Fatal("type label not reused")
	}
	c := objectID(t, mutateObject(t, s, p, "compositions", "c", "create", "", `{"type_id":"`+typ+`","name":"片头"}`))
	r := objectID(t, mutateObject(t, s, p, "resources", "r", "create", "", `{"composition_id":"`+c+`","description":null}`))
	v := objectID(t, mutateObject(t, s, p, "versions", "v", "create", "", `{"resource_id":"`+r+`"}`))
	v2 := objectID(t, mutateObject(t, s, p, "versions", "v2", "create", "", `{"resource_id":"`+r+`","source_version_id":"`+v+`"}`))
	r2 := objectID(t, mutateObject(t, s, p, "resources", "r2", "create", "", `{"composition_id":"`+c+`"}`))
	mutateObject(t, s, p, "versions", "derived", "create", "", `{"resource_id":"`+r2+`","source_version_id":"`+v2+`"}`)
	replay := mutateObject(t, s, p, "versions", "v2", "create", "", `{"source_version_id":"`+v+`","resource_id":"`+r+`"}`)
	if objectID(t, replay) != v2 {
		t.Fatal("replay created new version")
	}
	var n int
	if err := s.DB.QueryRow(`SELECT count(*) FROM versions WHERE resource_id=?`, r).Scan(&n); err != nil || n != 2 {
		t.Fatalf("identity count %d: %v", n, err)
	}
	_, err := s.Mutate(ctx, p, "versions", Mutation{RequestID: "v2", Action: "create", Value: json.RawMessage(`{"resource_id":"` + r2 + `"}`)})
	if !errors.Is(err, ErrRequestConflict) {
		t.Fatalf("different input: %v", err)
	}
	for kind, id := range map[string]string{"projects": p, "composition_types": typ, "compositions": c, "resources": r, "versions": v} {
		if _, err := s.Get(ctx, q, kind, id); err == nil {
			t.Fatalf("cross project GET %s", kind)
		}
		if _, err := s.Mutate(ctx, q, kind, Mutation{RequestID: "cross-" + kind, Action: "delete", ID: id}); err == nil {
			t.Fatalf("cross project delete %s", kind)
		}
	}
	for kind, id := range map[string]string{"projects": p, "composition_types": typ, "compositions": c, "resources": r, "versions": v} {
		_, err := s.Mutate(ctx, p, kind, Mutation{RequestID: "delete-" + kind, Action: "delete", ID: id})
		var domain *DomainError
		if !errors.As(err, &domain) || domain.Status != 409 {
			t.Fatalf("protected delete %s: %v", kind, err)
		}
	}
	_, err = s.Mutate(ctx, p, "versions", Mutation{RequestID: "update", Action: "update", ID: v2, Value: json.RawMessage(`{}`)})
	if err == nil {
		t.Fatal("mutable version")
	}
	for _, value := range []string{`{"type_id":"missing","name":"x"}`, `{"type_id":"` + typ + `","Name":"x"}`, `{"type_id":"` + typ + `","name":"x","extra":1}`} {
		if _, err = s.Mutate(ctx, p, "compositions", Mutation{RequestID: "invalid", Action: "create", Value: json.RawMessage(value)}); err == nil {
			t.Fatal("invalid create accepted")
		}
	}
}

func TestObjectsPaginationNullableMetadataAndAssets(t *testing.T) {
	ctx := context.Background()
	s, p, q := objectFixture(t)
	typ := objectID(t, mutateObject(t, s, p, "composition_types", "type", "create", "", `{"label":"视频"}`))
	c := objectID(t, mutateObject(t, s, p, "compositions", "c", "create", "", `{"type_id":"`+typ+`","name":"片头","attributes":"{\"设定\":\"中文\"}"}`))
	mutateObject(t, s, p, "compositions", "clear", "update", c, `{"attributes":null}`)
	raw, err := s.Get(ctx, p, "compositions", c)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	json.Unmarshal(raw, &out)
	if out["attributes"] != nil || out["name"] != "片头" {
		t.Fatalf("nullable update: %s", raw)
	}
	for _, value := range []string{`{"attributes":"[]"}`, `{"attributes":{}}`, `{"attributes":"broken"}`} {
		if _, err := s.Mutate(ctx, p, "compositions", Mutation{RequestID: "bad", Action: "update", ID: c, Value: json.RawMessage(value)}); err == nil {
			t.Fatalf("invalid metadata %s", value)
		}
	}
	for _, page := range []struct {
		offset, limit int
		bad           bool
	}{{-1, 1, true}, {0, 201, true}, {0, 0, false}, {0, 1, false}, {100, 1, false}} {
		got, err := s.List(ctx, p, "compositions", page.offset, page.limit)
		if (err != nil) != page.bad {
			t.Fatalf("page %+v: %v", page, err)
		}
		if !page.bad && got.Coverage.Total != 1 {
			t.Fatal("wrong total")
		}
	}
	// Trusted-file registration belongs to M2.3. Seed only to exercise existing asset protection here.
	_, err = s.DB.Exec(`INSERT INTO assets(id,path,size,mtime_ms,name,created_at,updated_at) VALUES('asset','managed.png',1,1,'PNG',1,1); INSERT INTO asset_projects VALUES('asset',?)`, p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(ctx, q, "assets", "asset"); err == nil {
		t.Fatal("cross project asset GET")
	}
	if _, err = s.Mutate(ctx, q, "assets", Mutation{RequestID: "cross", Action: "delete", ID: "asset"}); err == nil {
		t.Fatal("cross project asset DELETE")
	}
	mutateObject(t, s, p, "assets", "meta", "update", "asset", `{"metadata":"{}"}`)
	r := objectID(t, mutateObject(t, s, p, "resources", "r", "create", "", `{"composition_id":"`+c+`"}`))
	mutateObject(t, s, p, "versions", "v", "create", "", `{"resource_id":"`+r+`","asset_id":"asset"}`)
	if _, err = s.Mutate(ctx, p, "assets", Mutation{RequestID: "delete", Action: "delete", ID: "asset"}); err == nil {
		t.Fatal("referenced asset deleted")
	}
	if _, err = s.Mutate(ctx, p, "assets", Mutation{RequestID: "fake", Action: "create", Value: json.RawMessage(`{"path":"fake","quick_hash":"fake"}`)}); err == nil {
		t.Fatal("untrusted asset created")
	}
}

func TestObjectsReferencesDeletesAndInputIdentity(t *testing.T) {
	ctx := context.Background()
	s, p, q := objectFixture(t)
	typeID := objectID(t, mutateObject(t, s, p, "composition_types", "t", "create", "", `{"label":"test"}`))
	otherType := objectID(t, mutateObject(t, s, q, "composition_types", "t", "create", "", `{"label":"test"}`))
	c := objectID(t, mutateObject(t, s, p, "compositions", "c", "create", "", `{"type_id":"`+typeID+`","name":"c"}`))
	otherC := objectID(t, mutateObject(t, s, q, "compositions", "c", "create", "", `{"type_id":"`+otherType+`","name":"c"}`))
	r := objectID(t, mutateObject(t, s, p, "resources", "r", "create", "", `{"composition_id":"`+c+`"}`))
	otherR := objectID(t, mutateObject(t, s, q, "resources", "r", "create", "", `{"composition_id":"`+otherC+`"}`))
	v := objectID(t, mutateObject(t, s, p, "versions", "v", "create", "", `{"resource_id":"`+r+`"}`))
	otherV := objectID(t, mutateObject(t, s, q, "versions", "v", "create", "", `{"resource_id":"`+otherR+`"}`))
	_, err := s.DB.Exec(`INSERT INTO assets(id,path,size,mtime_ms,name,created_at,updated_at) VALUES('foreign','foreign.png',1,1,'foreign',1,1),('free','free.png',1,1,'free',1,1); INSERT INTO asset_projects VALUES('foreign',?),('free',?)`, q, p)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ kind, value string }{
		{"compositions", `{"type_id":"` + otherType + `","name":"x"}`},
		{"resources", `{"composition_id":"` + otherC + `"}`},
		{"versions", `{"resource_id":"` + otherR + `"}`},
		{"versions", `{"resource_id":"` + r + `","source_version_id":"` + otherV + `"}`},
		{"versions", `{"resource_id":"` + r + `","asset_id":"foreign"}`},
		{"versions", `{"resource_id":"` + r + `","asset_id":"missing"}`},
		{"versions", `{"resource_id":"` + r + `","source_version_id":"missing"}`},
	} {
		if _, err := s.Mutate(ctx, p, tc.kind, Mutation{RequestID: "bad-reference", Action: "create", Value: json.RawMessage(tc.value)}); err == nil {
			t.Fatalf("accepted reference: %s", tc.value)
		}
	}
	_, err = s.Mutate(ctx, p, "versions", Mutation{RequestID: "immutable", Action: "update", ID: v, Value: json.RawMessage(`{}`)})
	var d *DomainError
	if !errors.As(err, &d) || d.Status != 409 {
		t.Fatalf("version update: %v", err)
	}
	mutateObject(t, s, p, "assets", "metadata", "update", "free", `{"name":"自由","metadata":"{\"prompt\":\"  中文  \"}"}`)
	_, err = s.Mutate(ctx, p, "assets", Mutation{RequestID: "metadata", Action: "update", ID: "free", Value: json.RawMessage(`{"name":"自由","metadata":"{\"prompt\":\"中文\"}"}`)})
	if !errors.Is(err, ErrRequestConflict) {
		t.Fatalf("serialized content identity: %v", err)
	}
	mutateObject(t, s, p, "assets", "clear", "update", "free", `{"metadata":null}`)
	mutateObject(t, s, p, "assets", "delete", "delete", "free", "")
	raw, err := s.Get(ctx, p, "assets", "free")
	if err != nil {
		t.Fatal(err)
	}
	var asset Asset
	json.Unmarshal(raw, &asset)
	if asset.Status != "missing" || asset.Metadata != nil {
		t.Fatalf("soft-delete: %s", raw)
	}
	if _, err := s.Mutate(ctx, p, "versions", Mutation{RequestID: "missing-asset", Action: "create", Value: json.RawMessage(`{"resource_id":"` + r + `","asset_id":"free"}`)}); err == nil {
		t.Fatal("missing asset accepted")
	}
	for kind, id := range map[string]string{"composition_types": typeID, "compositions": c, "resources": r, "versions": v, "assets": "free", "projects": p} {
		_, err := s.Mutate(ctx, q, kind, Mutation{RequestID: "foreign-update", Action: "update", ID: id, Value: json.RawMessage(`{}`)})
		if !errors.As(err, &d) || d.Status != 404 {
			t.Fatalf("foreign update %s: %v", kind, err)
		}
	}
	// Member placement protects either endpoint even when there are no versions.
	emptyR := objectID(t, mutateObject(t, s, p, "resources", "empty-r", "create", "", `{"composition_id":"`+c+`"}`))
	emptyC := objectID(t, mutateObject(t, s, p, "compositions", "empty-c", "create", "", `{"type_id":"`+typeID+`","name":"empty"}`))
	if _, err = s.DB.Exec(`INSERT INTO composition_members(composition_id,resource_id,role) VALUES(?,?,'cover')`, emptyC, emptyR); err != nil {
		t.Fatal(err)
	}
	for kind, id := range map[string]string{"compositions": emptyC, "resources": emptyR} {
		_, err = s.Mutate(ctx, p, kind, Mutation{RequestID: "member", Action: "delete", ID: id})
		if !errors.As(err, &d) || d.Status != 409 {
			t.Fatalf("member protection %s: %v", kind, err)
		}
	}
	mutateObject(t, s, p, "versions", "delete-v", "delete", v, "")
	mutateObject(t, s, p, "resources", "delete-r", "delete", r, "")
	if _, err = s.Get(ctx, p, "resources", r); err == nil {
		t.Fatal("resource not deleted")
	}
	for _, input := range []string{`{"requestId":"i","action":"create","id":"","value":{"name":"x"}}`, `{"requestId":"i","action":"delete","id":"x","value":null}`} {
		if _, err := DecodeValue[Mutation]([]byte(input)); err == nil {
			t.Fatalf("lossy input accepted: %s", input)
		}
	}
	for _, input := range []string{`{"requestId":"i","Action":"create","value":{"name":"x"}}`, `{"requestId":"i","action":"create","extra":1,"value":{"name":"x"}}`} {
		if _, err := DecodeValue[Mutation]([]byte(input)); err == nil {
			t.Fatalf("custom mutation decoder bypassed exact schema: %s", input)
		}
	}
}

func TestObjectsProjectTypeCrudAndPageCoverage(t *testing.T) {
	ctx := context.Background()
	s, p, _ := objectFixture(t)
	mutateObject(t, s, p, "projects", "rename", "update", p, `{"name":"新项目"}`)
	typeID := objectID(t, mutateObject(t, s, p, "composition_types", "type-a", "create", "", `{"label":"a"}`))
	mutateObject(t, s, p, "composition_types", "type-b", "create", "", `{"label":"b"}`)
	_, err := s.Mutate(ctx, p, "composition_types", Mutation{RequestID: "rename-collision", Action: "update", ID: typeID, Value: json.RawMessage(`{"label":"b"}`)})
	var d *DomainError
	if !errors.As(err, &d) || d.Status != 409 {
		t.Fatalf("type collision: %v", err)
	}
	mutateObject(t, s, p, "composition_types", "rename-type", "update", typeID, `{"label":"c"}`)
	for _, tc := range []struct {
		offset, wantLen, next int
		exhausted             bool
	}{{0, 1, 1, false}, {1, 1, 2, true}, {2, 0, 2, true}} {
		page, err := s.List(ctx, p, "composition_types", tc.offset, 1)
		if err != nil || len(page.Items) != tc.wantLen || page.Coverage.Total != 2 || page.Coverage.NextOffset != tc.next || page.Coverage.Exhausted != tc.exhausted {
			t.Fatalf("coverage %+v: %+v %v", tc, page, err)
		}
	}
	mutateObject(t, s, p, "composition_types", "delete-type", "delete", typeID, `{}`)
	if _, err = s.Get(ctx, p, "composition_types", typeID); err == nil {
		t.Fatal("type delete failed")
	}
	empty, err := s.CreateProject(ctx, "empty", "empty")
	if err != nil {
		t.Fatal(err)
	}
	first := mutateObject(t, s, empty.ID, "projects", "delete-empty", "delete", empty.ID, "")
	replay := mutateObject(t, s, empty.ID, "projects", "delete-empty", "delete", empty.ID, "")
	if string(first) != string(replay) {
		t.Fatal("deleted project receipt did not replay")
	}
	if _, err = s.Get(ctx, empty.ID, "projects", empty.ID); err == nil {
		t.Fatal("project delete failed")
	}
}
