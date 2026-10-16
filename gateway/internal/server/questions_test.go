package server

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/workspace"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A real HTTP reply must be the only transition that unlocks a provider POST.
func TestQuestionBillingHTTPGate(t *testing.T) {
	for _, mode := range []string{"approve", "reject", "expired", "revision", "scope", "session", "config", "chat", "zero", "retry", "receipt-recovery", "numeric", "task-revision", "input-hash", "credential", "body-actor", "unpriced", "generic", "reject-missing-reference"} {
		t.Run(mode, func(t *testing.T) {
			databasePath := filepath.Join(t.TempDir(), "gate.db")
			db, e := store.Open(databasePath)
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			ws, e := workspace.Open(t.TempDir())
			if e != nil {
				t.Fatal(e)
			}
			defer ws.Close()
			ps := providers.NewService(db, nil)
			var posts atomic.Int32
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				posts.Add(1)
				if mode == "numeric" {
					b, _ := io.ReadAll(r.Body)
					if !strings.Contains(string(b), `"output_compression":1e0`) {
						t.Error("numeric token changed", string(b))
					}
				}
				if mode == "retry" {
					w.WriteHeader(400)
					w.Write([]byte(`{"error":{"message":"fixture rejection"}}`))
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"data":[{"b64_json":"fixture"}]}`))
			}))
			defer remote.Close()
			amount := int64(7)
			if mode == "zero" {
				amount = 0
			}
			cfg := providers.Config{ID: "local", Name: "Local", Kind: "image", Protocol: "openai-images", BaseURL: remote.URL, ModelID: "fixture", Enabled: true, Capability: providers.Capability{MaxPromptBytes: 65536, MaxCount: 1, MaxReferences: 0, Sizes: []string{"1024x1024"}}, Pricing: &providers.Pricing{Currency: "USD", Unit: "image", UnitAmount: amount, MinorUnitScale: 2, CreditNumerator: 2, CreditDenominator: 3, Source: "fixture", Version: "v1"}}
			if mode == "unpriced" {
				cfg.Pricing = nil
			}
			raw, _ := json.Marshal(map[string]any{"requestId": "provider", "action": "create", "config": cfg})
			if _, e = ps.Execute(context.Background(), raw); e != nil {
				t.Fatal(e)
			}
			project, e := (store.Objects{DB: db}).CreateProject(context.Background(), "project", "Gate")
			if e != nil {
				t.Fatal(e)
			}
			h := newHandler("http://tauri.localhost", db, workspace.NewService(db, ws), ps).(*Server)
			defer h.Close()
			call := func(method, path string, body any) *httptest.ResponseRecorder {
				b, _ := json.Marshal(body)
				r := httptest.NewRequest(method, path, bytes.NewReader(b))
				r.Header.Set("Origin", "http://tauri.localhost")
				r.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				return w
			}
			base := "?project_id=" + project.ID
			params := map[string]any{}
			if mode == "numeric" {
				params["output_format"] = "jpeg"
				params["output_compression"] = json.Number("1e0")
			}
			generation := map[string]any{"params": params, "requestId": "generation", "session_id": "session-a", "capability_id": "image:local", "prompt": "literal 同意", "output": map[string]any{"resources": []any{map[string]any{"key": "main", "mode": "create", "name": "Result"}}, "composition": map[string]any{"name": "Result", "type": map[string]any{"label": "Images"}, "placements": []any{map[string]any{"key": "main", "role": "primary"}}}}}
			if mode == "numeric" {
				params["output_compression"] = json.Number("1.0000000000000001")
				bad := call("POST", "/api/generate/image"+base, generation)
				if bad.Code != 400 {
					t.Fatal("fractional integer admitted", bad.Code, bad.Body.String())
				}
				params["output_compression"] = json.Number("1e0")
			}
			gen := call("POST", "/api/generate/image"+base, generation)
			if mode == "unpriced" {
				if gen.Code != 400 || posts.Load() != 0 {
					t.Fatal("unpriced generation admitted", gen.Code)
				}
				return
			}
			if gen.Code != 202 {
				t.Fatal(gen.Code, gen.Body.String())
			}
			var accepted map[string]string
			json.Unmarshal(gen.Body.Bytes(), &accepted)
			id := accepted["task_id"]
			list := call("GET", "/api/question"+base+"&session_id=session-a", nil)
			if list.Code != 200 {
				t.Fatalf("pending billing card unavailable: %d %s", list.Code, list.Body.String())
			}
			var qs struct {
				Items []struct {
					ID       string          `json:"id"`
					Revision int64           `json:"revision"`
					Quote    providers.Quote `json:"quote"`
				} `json:"items"`
			}
			json.Unmarshal(list.Body.Bytes(), &qs)
			if len(qs.Items) != 1 {
				t.Fatal(list.Body.String())
			}
			q := qs.Items[0]
			if q.Quote.EstimatedAmount != amount || q.Quote.InputHash == "" {
				t.Fatal(q)
			}
			if e = h.tasks.Run(context.Background(), project.ID, id); e != nil {
				t.Fatal(e)
			}
			if posts.Load() != 0 {
				t.Fatal("unapproved POST")
			}
			endpoint := "/api/question/" + q.ID + "/reply" + base + "&session_id=session-a"
			reply := map[string]any{"requestId": "human-intent", "expected_revision": q.Revision, "answers": [][]string{{"确认生成"}}}
			want := 200
			switch mode {
			case "reject", "reject-missing-reference":
				if mode == "reject-missing-reference" {
					db.Exec(`UPDATE tasks SET frozen_input_json=json_set(frozen_input_json,'$.references',json('[{"asset_id":"missing","role":"reference"}]')) WHERE id=?`, id)
				}
				endpoint = "/api/question/" + q.ID + "/reject" + base + "&session_id=session-a"
				reply["answers"] = [][]string{}
			case "expired":
				db.Exec("UPDATE price_quotes SET expires_at=0 WHERE id=?", q.Quote.QuoteID)
				want = 409
			case "revision":
				reply["expected_revision"] = q.Revision + 1
				want = 409
			case "scope":
				endpoint = "/api/question/" + q.ID + "/reply?project_id=other&session_id=session-a"
				want = 404
			case "session":
				endpoint = "/api/question/" + q.ID + "/reply" + base + "&session_id=other"
				want = 404
			case "config":
				db.Exec("UPDATE provider_configs SET config_json=json_set(config_json,'$.pricing.unit_amount',8)")
				want = 409
			case "generic":
				generic := call("POST", "/api/question"+base, map[string]any{"requestId": "generic", "kind": "choice", "session_id": "session-a", "task_id": id, "task_revision": 1, "questions": []any{map[string]any{"header": "Decision", "question": "Pick text", "options": []any{map[string]string{"label": "确认生成", "description": "ordinary label, no billing authority"}}}}})
				if generic.Code != 200 {
					t.Fatal(generic.Code, generic.Body.String())
				}
				var g struct {
					ID string `json:"id"`
				}
				json.Unmarshal(generic.Body.Bytes(), &g)
				endpoint = "/api/question/" + g.ID + "/reply" + base + "&session_id=session-a"
			case "task-revision":
				db.Exec("UPDATE tasks SET revision=revision+1,view_json=json_set(view_json,'$.revision',revision+1) WHERE id=?", id)
				want = 409
			case "input-hash":
				db.Exec("UPDATE tasks SET frozen_input_json=json_set(frozen_input_json,'$.prompt','changed') WHERE id=?", id)
				want = 409
			case "credential":
				db.Exec("UPDATE provider_configs SET credential_version='changed'")
				want = 409
			case "body-actor":
				reply["actor"] = "human"
				want = 400
			case "chat":
				reply["answers"] = [][]string{{"同意"}}
				want = 400
			}
			if mode == "receipt-recovery" {
				h.tasks.Executor = nil
			}
			result := call("POST", endpoint, reply)
			if result.Code != want {
				t.Fatalf("reply %d want %d: %s", result.Code, want, result.Body.String())
			}
			if mode == "expired" {
				renewal := map[string]any{"requestId": "renewal", "kind": "billing", "session_id": "session-a", "task_id": id, "task_revision": 1}
				renewed := call("POST", "/api/question"+base, renewal)
				if renewed.Code != 200 {
					t.Fatal("explicit renewal", renewed.Code, renewed.Body.String())
				}
				var card struct {
					ID    string          `json:"id"`
					Quote providers.Quote `json:"quote"`
				}
				json.Unmarshal(renewed.Body.Bytes(), &card)
				if card.ID == q.ID || card.Quote.QuoteID == q.Quote.QuoteID {
					t.Fatal("renewal reused expired intent")
				}
				repeated := call("POST", "/api/question"+base, renewal)
				if repeated.Body.String() != renewed.Body.String() {
					t.Fatal("renewal replay changed card")
				}
				if stale := call("POST", endpoint, reply); stale.Code != 409 {
					t.Fatal("superseded card accepted", stale.Code)
				}
			}

			if mode == "receipt-recovery" {
				if posts.Load() != 0 {
					t.Fatal("fixture should stop before worker")
				}
				h.Close()
				db.Close()
				db, e = store.Open(databasePath)
				if e != nil {
					t.Fatal(e)
				}
				defer db.Close()
				ps = providers.NewService(db, nil)
				h = newHandler("http://tauri.localhost", db, workspace.NewService(db, ws), ps).(*Server)
				defer h.Close()
				if e = h.tasks.Recover(context.Background()); e != nil {
					t.Fatal(e)
				}
				if posts.Load() != 0 {
					t.Fatal("startup submitted automatically")
				}
				recovered := call("POST", endpoint, reply)
				if recovered.Code != 200 || recovered.Body.String() != result.Body.String() {
					t.Fatal("lost approval receipt", recovered.Code, recovered.Body.String())
				}
			}
			if mode == "approve" || mode == "zero" || mode == "retry" || mode == "receipt-recovery" || mode == "numeric" {
				deadline := time.Now().Add(3 * time.Second)
				for posts.Load() < 1 && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond * 10)
				}
				if posts.Load() != 1 {
					t.Fatal("approval did not submit exactly once", posts.Load())
				}
				duplicate := call("POST", endpoint, reply)
				if duplicate.Code != 200 || duplicate.Body.String() != result.Body.String() {
					t.Fatal("original receipt not replayed", duplicate.Code, duplicate.Body.String())
				}
				h.tasks.Run(context.Background(), project.ID, id)
				if posts.Load() != 1 {
					t.Fatal("replayed approval resubmitted")
				}
				if mode == "retry" {
					var rev int64
					for time.Now().Before(deadline) {
						v, _ := h.tasks.Query(context.Background(), project.ID, id)
						rev = v.Revision
						if v.Status == "failed" {
							break
						}
						time.Sleep(10 * time.Millisecond)
					}
					next := call("POST", "/api/tasks/"+id+"/resolve"+base, map[string]any{"requestId": "retry", "expected_revision": rev, "action": "retry"})
					if next.Code != 200 {
						t.Fatal(next.Code, next.Body.String())
					}
					cards := call("GET", "/api/question"+base+"&session_id=session-a", nil)
					json.Unmarshal(cards.Body.Bytes(), &qs)
					if len(qs.Items) != 2 || qs.Items[1].Quote.QuoteID == q.Quote.QuoteID {
						t.Fatal("retry reused old approval/quote", cards.Body.String())
					}
					h.tasks.Run(context.Background(), project.ID, id)
					if posts.Load() != 1 {
						t.Fatal("retry bypassed card")
					}
				}
			} else {
				h.tasks.Run(context.Background(), project.ID, id)
				if posts.Load() != 0 {
					t.Fatal("invalid reply submitted")
				}
				if mode == "reject" || mode == "reject-missing-reference" {
					v, _ := h.tasks.Query(context.Background(), project.ID, id)
					if v.Status != "cancelled" {
						t.Fatal(v)
					}
				}
			}
		})
	}
}
