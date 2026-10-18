package server

import (
	"context"
	"encoding/json"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestChatListExistsBeforeNativeConfiguration(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	h := newHandler("http://tauri.localhost", db, nil).(*Server)
	defer h.Close()
	r := httptest.NewRequest("GET", "/api/chat/sessions?project_id=missing", nil)
	r.Header.Set("Origin", "http://tauri.localhost")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code == 404 && w.Body.String() == "{\"error\":{\"code\":\"not_found\",\"message\":\"Route not found\"}}\n" {
		t.Fatal("durable chat routes absent")
	}
}

func TestChatHTTPPaginationRejectsUnknownWireAndOrigin(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	h := newHandler("http://tauri.localhost", db, nil).(*Server)
	defer h.Close()
	p, e := (store.Objects{DB: db}).CreateProject(context.Background(), "project", "P")
	if e != nil {
		t.Fatal(e)
	}
	for _, id := range []string{"one", "two"} {
		_, e = h.chat.Create(context.Background(), p.ID, []byte(`{"requestId":"`+id+`","title":"Chat"}`))
		if e != nil {
			t.Fatal(e)
		}
	}
	r := httptest.NewRequest("GET", "/api/chat/sessions?project_id="+p.ID+"&offset=1&limit=1", nil)
	r.Header.Set("Origin", "http://tauri.localhost")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var page store.Page
	if e = json.Unmarshal(w.Body.Bytes(), &page); e != nil || w.Code != 200 || len(page.Items) != 1 || page.Coverage.Total != 2 || page.Coverage.NextOffset != 2 || !page.Coverage.Exhausted {
		t.Fatal("chat pagination missing", w.Code, w.Body.String())
	}
	var session struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(page.Items[0], &session)
	h.chat.Grace = time.Millisecond
	path := "/api/chat/sessions/" + session.ID + "/messages?project_id=" + p.ID
	body := `{"requestId":"send","document":{"type":"doc","content":[]},"text":"hello","references":[],"annotations":[]}`
	call := func(method, path, body, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, origin := range []string{"", "null", "https://evil.example"} {
		if call("POST", path, body, origin).Code != 403 {
			t.Fatal("chat Origin bypass")
		}
	}
	first := call("POST", path, body, "http://tauri.localhost")
	if first.Code != 202 {
		t.Fatal("chat send", first.Code, first.Body.String())
	}
	replay := call("POST", path, body, "http://tauri.localhost")
	if replay.Code != 202 || first.Body.String() != replay.Body.String() {
		t.Fatal("chat replay changed")
	}
	for _, invalid := range []string{strings.Replace(body, "hello", "changed", 1), strings.Replace(body, `"requestId":"send"`, `"requestId":"other","actor":"human"`, 1)} {
		if call("POST", path, invalid, "http://tauri.localhost").Code < 400 {
			t.Fatal("mutated/forged write accepted")
		}
	}
	for _, query := range []string{"&offset=01", "&limit=0", "&limit=101", "&offset=1&offset=2", "&unknown=x"} {
		if call("GET", path+query, "", "http://tauri.localhost").Code != 400 {
			t.Fatal("invalid pagination", query)
		}
	}
	read := call("GET", path+"&offset=0&limit=1", "", "http://tauri.localhost")
	if read.Code != 200 || !strings.Contains(read.Body.String(), `"total":1`) {
		t.Fatal("message read route", read.Body.String())
	}
	if call("POST", "/internal/inference/responses", `{}`, "http://tauri.localhost").Code == 200 {
		t.Fatal("public client reached private inference")
	}
}
