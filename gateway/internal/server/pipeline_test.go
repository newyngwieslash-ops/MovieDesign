package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/testsupport"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoryboardHTTP(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	p, e := (store.Objects{DB: db}).CreateProject(context.Background(), "project", "Film")
	if e != nil {
		t.Fatal(e)
	}
	h := newTestServer(t, "http://tauri.localhost", db, t.TempDir())
	raw := `{"requestId":"first","mode":"create","format":"md","typedDocument":{"kind":"storyboard","pipelineId":"film","expectedRevision":0,"storyboard":{"panels":[{"id":"p","timeMs":0,"framing":"close","action":"speak","transition":"continuous"}],"shots":[{"id":"s","startMs":0,"endMs":2000,"panelIds":["p"],"narrativeRole":"intro","startState":"start","endState":"end"}],"timeline":{"durationMs":2000,"shotIds":["s"],"audioIntent":"none"}}},"output":{"resources":[{"key":"r","mode":"create","name":"review"}],"composition":{"name":"Film","type":{"label":"video"},"placements":[{"key":"r","role":"storyboard"}]}}}`
	call := func(method, path, body, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Origin", origin)
		r.Header.Set("Access-Control-Request-Method", "POST")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	url := "/api/documents/write?project_id=" + p.ID
	for _, tc := range []struct {
		method, path, body, origin string
		status                     int
	}{{"POST", url, raw, "bad", 403}, {"GET", url, "", "http://tauri.localhost", 405}, {"OPTIONS", url, "", "http://tauri.localhost", 204}, {"POST", url + "&project_id=x", raw, "http://tauri.localhost", 400}, {"POST", url, strings.Replace(raw, `"timeMs":0`, `"timeMs":null`, 1), "http://tauri.localhost", 400}, {"POST", url, strings.Replace(raw, `"timeMs":0`, `"timeMs":0,"extra":true`, 1), "http://tauri.localhost", 400}, {"POST", url, strings.Repeat(" ", 1<<20) + raw, "http://tauri.localhost", 413}} {
		w := call(tc.method, tc.path, tc.body, tc.origin)
		if w.Code != tc.status {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
	}
	testsupport.CreativeUpstream(t, db, p.ID, "film", func(b []byte) error {
		w := call("POST", url, string(b), "http://tauri.localhost")
		if w.Code != 200 {
			return fmt.Errorf("%d %s", w.Code, w.Body.String())
		}
		return nil
	})
	w := call("POST", url, raw, "http://tauri.localhost")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var r struct {
		Delivery struct {
			Versions []struct {
				VersionID string `json:"versionId"`
			}
		}
	}
	json.Unmarshal(w.Body.Bytes(), &r)
	version := r.Delivery.Versions[0].VersionID
	get := "/api/pipeline/storyboard?project_id=" + p.ID + "&versionId=" + version
	w = call("GET", get, "", "http://tauri.localhost")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"approvalStatus":"unapproved"`) {
		t.Fatal(w.Code, w.Body.String())
	}

	// Direct public Version creation must not bypass registered creative semantics.
	var resource string
	if e = db.QueryRow("SELECT resource_id FROM versions WHERE id=?", version).Scan(&resource); e != nil {
		t.Fatal(e)
	}
	direct := fmt.Sprintf(`{"requestId":"bypass","action":"create","value":{"resource_id":%q,"source_version_id":%q}}`, resource, version)
	if got := call("POST", "/api/versions?project_id="+p.ID, direct, "http://tauri.localhost"); got.Code != 400 {
		t.Fatalf("direct registered Version bypass: %d %s", got.Code, got.Body.String())
	}
	binding := call("GET", "/api/pipeline/artifact?project_id="+p.ID+"&versionId="+version, "", "http://tauri.localhost")
	proposal := fmt.Sprintf(`{"requestId":"human-card","session_id":"review","kind":"creative","creative":%s}`, binding.Body.String())
	card := call("POST", "/api/question?project_id="+p.ID, proposal, "http://tauri.localhost")
	if card.Code != 200 {
		t.Fatal(card.Code, card.Body.String())
	}
	var q struct {
		ID string `json:"id"`
	}
	if e = json.Unmarshal(card.Body.Bytes(), &q); e != nil {
		t.Fatal(e)
	}
	reply := call("POST", "/api/question/"+q.ID+"/reply?project_id="+p.ID+"&session_id=review", `{"requestId":"human","expected_revision":1,"answers":[["批准分镜"]]}`, "http://tauri.localhost")
	if reply.Code != 200 {
		t.Fatal(reply.Code, reply.Body.String())
	}
	if n := call("GET", get, "", "http://tauri.localhost"); !strings.Contains(n.Body.String(), `"approvalStatus":"approved"`) {
		t.Fatal(n.Body.String())
	}
	w = call("GET", strings.Replace(get, p.ID, "other", 1), "", "http://tauri.localhost")
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
}
