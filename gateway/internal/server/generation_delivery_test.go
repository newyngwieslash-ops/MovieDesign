package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/tasks"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/workspace"
)

// Real human HTTP approval, SQLite journals and real managed PNG bytes. Removing
// collection, reserved identity, commit replay or cancellation guards breaks this.
func TestGenerationDelivery(t *testing.T) {
	for _, mode := range []string{"complete", "receipt-restart", "invalid-media", "bad-count", "download-retry", "partial-body", "commit-restart", "cancel-download", "cancel-archive", "adopt-late", "adopt-archive-delete", "adopt-archive-rename", "late-version", "unknown", "reject", "target-failed-existing-type", "target-failed-existing-composition", "target-failed-new-type", "target-failed-new-composition", "target-cancelled-existing-type", "target-cancelled-existing-composition", "target-cancelled-new-type", "target-cancelled-new-composition", "target-unsubmitted-existing-type", "target-unsubmitted-existing-composition", "target-unsubmitted-new-type", "target-unsubmitted-new-composition", "target-invalid-existing-type", "target-invalid-existing-composition", "target-invalid-new-type", "target-invalid-new-composition", "target-notaccepted-existing-type", "target-notaccepted-existing-composition", "target-notaccepted-new-type", "target-notaccepted-new-composition", "target-adoption-invalid-existing-type", "target-adoption-invalid-existing-composition", "target-adoption-invalid-new-type", "target-adoption-invalid-new-composition"} {
		t.Run(mode, func(t *testing.T) {
			archiveAdoption := strings.HasPrefix(mode, "adopt-archive")
			targetCase := strings.HasPrefix(mode, "target-")
			adoptionInvalid := strings.HasPrefix(mode, "target-adoption-invalid-")
			cancelTarget := strings.HasPrefix(mode, "target-cancelled-") || strings.HasPrefix(mode, "target-notaccepted-") || adoptionInvalid
			failedTarget := strings.HasPrefix(mode, "target-failed-")
			ctx := context.Background()
			db, e := store.Open(filepath.Join(t.TempDir(), "generation.db"))
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			ws, e := workspace.Open(t.TempDir())
			if e != nil {
				t.Fatal(e)
			}
			defer ws.Close()
			project, e := (store.Objects{DB: db}).CreateProject(ctx, "project", "Generation")
			if e != nil {
				t.Fatal(e)
			}
			var pngBytes bytes.Buffer
			png.Encode(&pngBytes, image.NewNRGBA(image.Rect(0, 0, 4, 4)))
			var posts, downloads atomic.Int32
			entered, release := make(chan struct{}), make(chan struct{})
			var remote *httptest.Server
			remote = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" {
					posts.Add(1)
					if cancelTarget {
						close(entered)
						<-release
					}
					if mode == "invalid-media" || strings.HasPrefix(mode, "target-invalid-") || adoptionInvalid {
						json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]string{"b64_json": "invalid base64!"}}})
						return
					}
					if mode == "bad-count" {
						json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
						return
					}
					if mode == "unknown" {
						w.WriteHeader(500)
						return
					}
					if mode == "reject" || strings.HasPrefix(mode, "target-notaccepted-") {
						w.WriteHeader(400)
						return
					}
					json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]string{"url": remote.URL + "/result?signature=private-result-token"}}})
					return
				}
				n := downloads.Add(1)
				if (mode == "download-retry" || failedTarget) && n == 1 {
					w.WriteHeader(503)
					return
				}
				if mode == "partial-body" && n == 1 {
					w.Header().Set("Content-Length", fmt.Sprint(pngBytes.Len()+100))
					w.Header().Set("Content-Type", "image/png")
					w.Write(pngBytes.Bytes()[:10])
					return
				}
				if mode == "cancel-download" || mode == "adopt-late" {
					close(entered)
					<-release
				}
				w.Header().Set("Content-Type", "image/png")
				w.Write(pngBytes.Bytes())
			}))
			defer remote.Close()
			ps := providers.NewService(db, nil)
			cfg := providers.Config{ID: "fixture", Name: "Fixture", Kind: "image", Protocol: "openai-images", BaseURL: remote.URL, ModelID: "fixture", Enabled: true, Capability: providers.Capability{MaxPromptBytes: 65536, MaxCount: 1}, Pricing: &providers.Pricing{Currency: "USD", Unit: "image", UnitAmount: 7, MinorUnitScale: 2, CreditDenominator: 1, Source: "fixture", Version: "v1"}}
			raw, _ := json.Marshal(map[string]any{"requestId": "provider", "action": "create", "config": cfg})
			if _, e = ps.Execute(ctx, raw); e != nil {
				t.Fatal(e)
			}
			h := newHandler("http://tauri.localhost", db, workspace.NewService(db, ws), ps).(*Server)
			defer func() { h.Close() }()
			call := func(path string, body any) *httptest.ResponseRecorder {
				b, _ := json.Marshal(body)
				r := httptest.NewRequest("POST", path+"?project_id="+project.ID, bytes.NewReader(b))
				r.Header.Set("Origin", "http://tauri.localhost")
				r.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				return w
			}
			createObject := func(path, key string, value any) string {
				out := call(path, map[string]any{"requestId": key, "action": "create", "value": value})
				var object struct{ ID string }
				if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &object) != nil || object.ID == "" {
					t.Fatal("create object", path, out.Code, out.Body.String())
				}
				return object.ID
			}
			declaration := map[string]string{"key": "main", "mode": "create", "name": "Image"}
			var targetType, targetComposition string
			if targetCase && (strings.Contains(mode, "-existing-") || strings.HasSuffix(mode, "new-composition")) {
				targetType = createObject("/api/composition-types", "target-type", map[string]string{"label": "Images"})
				if strings.Contains(mode, "-existing-") {
					targetComposition = createObject("/api/compositions", "target-composition", map[string]string{"type_id": targetType, "name": "Scene"})
				}
			}
			var existingResource, newer string
			if mode == "late-version" || archiveAdoption {
				typeID := createObject("/api/composition-types", "type", map[string]string{"label": "Images"})
				compositionID := createObject("/api/compositions", "composition", map[string]string{"type_id": typeID, "name": "Scene"})
				existingResource = createObject("/api/resources", "resource", map[string]string{"composition_id": compositionID})
				base := createObject("/api/versions", "base", map[string]string{"resource_id": existingResource})
				declaration["mode"], declaration["baseVersionId"] = "new_version", base
			}
			gen := call("/api/generate/image", map[string]any{"requestId": "generation", "capability_id": "image:fixture", "prompt": "exact original", "output": map[string]any{"resources": []any{declaration}, "composition": map[string]any{"name": "Scene", "type": map[string]string{"label": "Images"}, "placements": []any{map[string]string{"key": "main", "role": "primary"}}}}})
			if gen.Code != 202 {
				t.Fatal(gen.Code, gen.Body.String())
			}
			var accepted map[string]string
			json.Unmarshal(gen.Body.Bytes(), &accepted)
			id := accepted["task_id"]
			var qid, version, resource string
			var ordinal int
			db.QueryRow("SELECT id FROM questions WHERE task_id=?", id).Scan(&qid)
			if e = db.QueryRow("SELECT json_extract(source_json,'$.version_id'),json_extract(source_json,'$.resource_id'),json_extract(source_json,'$.ordinal') FROM task_outputs WHERE task_id=?", id).Scan(&version, &resource, &ordinal); e != nil {
				t.Fatal(e)
			}
			if posts.Load() != 0 {
				t.Fatal("unapproved submission")
			}
			if existingResource != "" {
				newer = createObject("/api/versions", "newer", map[string]string{"resource_id": existingResource})
			}
			checkCurrent := func() {
				var current string
				var oldOrdinal, newerOrdinal, highwater, unique, count int
				if e = db.QueryRow("SELECT current_version_id,next_ordinal FROM canvas_resource_state WHERE resource_id=?", resource).Scan(&current, &highwater); e != nil {
					t.Fatal(e)
				}
				if newer == "" {
					if current != version {
						t.Fatal("forward delivery did not advance current version", current)
					}
					return
				}
				db.QueryRow("SELECT ordinal FROM canvas_version_state WHERE version_id=?", version).Scan(&oldOrdinal)
				db.QueryRow("SELECT ordinal FROM canvas_version_state WHERE version_id=?", newer).Scan(&newerOrdinal)
				db.QueryRow("SELECT count(*),count(DISTINCT ordinal) FROM canvas_version_state WHERE resource_id=?", resource).Scan(&count, &unique)
				if current != newer || oldOrdinal != ordinal || oldOrdinal >= newerOrdinal || highwater <= newerOrdinal || count != unique {
					t.Fatalf("late reserved version replaced newer current: current=%s want=%s ordinals=%d/%d highwater=%d unique=%d/%d", current, newer, oldOrdinal, newerOrdinal, highwater, unique, count)
				}
			}
			if mode == "commit-restart" {
				_, e = db.Exec(`CREATE TRIGGER generation_fault BEFORE UPDATE OF stage ON deliveries WHEN NEW.stage='completed' BEGIN SELECT RAISE(ABORT,'fixture commit failure'); END`)
				if e != nil {
					t.Fatal(e)
				}
			}
			if mode == "cancel-archive" || archiveAdoption {
				_, e = db.Exec(`CREATE TRIGGER generation_cancel AFTER UPDATE OF stage ON delivery_sources WHEN NEW.stage='archived' BEGIN UPDATE tasks SET status='cancelled',revision=revision+1,view_json=json_set(view_json,'$.status','cancelled','$.revision',revision+1,'$.actions',json('[]')) WHERE id='` + id + `'; END`)
				if e != nil {
					t.Fatal(e)
				}
			}
			checkTarget := func(protected bool) {
				body := map[string]any{"requestId": fmt.Sprintf("target-write-%t", protected), "action": "create", "value": map[string]string{"label": "Images"}}
				path := "/api/composition-types"
				if strings.HasSuffix(mode, "composition") {
					path = "/api/compositions"
					body["value"] = map[string]string{"type_id": targetType, "name": "Scene"}
					if targetComposition != "" {
						body["action"], body["id"], body["value"] = "update", targetComposition, map[string]string{"name": "Renamed"}
					}
				} else if targetType != "" {
					body["action"], body["id"], body["value"] = "update", targetType, map[string]string{"label": "Renamed"}
				}
				out := call(path, body)
				want := 200
				if protected {
					want = 409
				}
				if out.Code != want {
					t.Fatalf("target reservation protected=%t: got %d want %d: %s", protected, out.Code, want, out.Body.String())
				}
			}
			if strings.HasPrefix(mode, "target-unsubmitted-") {
				view, _ := h.tasks.Query(ctx, project.ID, id)
				out := call("/api/tasks/"+id+"/resolve", map[string]any{"requestId": "cancel-unsent", "action": "cancel", "expected_revision": view.Revision})
				if out.Code != 200 {
					t.Fatal(out.Code, out.Body.String())
				}
				checkTarget(false)
				if posts.Load() != 0 {
					t.Fatal("unsubmitted cancellation sent provider POST")
				}
				return
			}
			reply := call("/api/question/"+qid+"/reply", map[string]any{"requestId": "approval", "expected_revision": 1, "answers": [][]string{{"确认生成"}}})
			if reply.Code != 200 {
				t.Fatal(reply.Code, reply.Body.String())
			}
			if mode == "cancel-download" || mode == "adopt-late" {
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					t.Fatal("download never started")
				}
				v, _ := h.tasks.Query(ctx, project.ID, id)
				cancel := call("/api/tasks/"+id+"/resolve", map[string]any{"requestId": "cancel", "action": "cancel", "expected_revision": v.Revision})
				close(release)
				if cancel.Code != 200 {
					t.Fatal(cancel.Code, cancel.Body.String())
				}
			}

			if cancelTarget {
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					t.Fatal("provider not started")
				}
				view, _ := h.tasks.Query(ctx, project.ID, id)
				cancel := call("/api/tasks/"+id+"/resolve", map[string]any{"requestId": "cancel-target", "action": "cancel", "expected_revision": view.Revision})
				// Release before asserting so a failed reservation cannot strand the fixture.
				if cancel.Code != 200 {
					close(release)
					t.Fatal(cancel.Code, cancel.Body.String())
				}
				func() { defer close(release); checkTarget(true) }()
			}
			deadline := time.Now().Add(6 * time.Second)
			var v tasks.TaskView
			for time.Now().Before(deadline) {
				v, _ = h.tasks.Query(ctx, project.ID, id)
				if tasks.Terminal(v.Status) || v.Status == tasks.Unknown {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if strings.HasPrefix(mode, "target-notaccepted-") {
				var state string
				for deadline = time.Now().Add(4 * time.Second); time.Now().Before(deadline); {
					db.QueryRow("SELECT state FROM task_attempts WHERE task_id=?", id).Scan(&state)
					if state == "failed" {
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
				if state != "failed" || posts.Load() != 1 {
					t.Fatal("cancelled rejection did not settle", state, posts.Load())
				}
				checkTarget(false)
				return
			}
			if strings.HasPrefix(mode, "target-invalid-") {
				if v.Status != tasks.Failed || v.Problem == nil || v.Problem.Retryable {
					t.Fatal("expected immutable failure", v)
				}
				checkTarget(false)
				return
			}
			if mode == "download-retry" || mode == "partial-body" || mode == "commit-restart" || failedTarget {
				if v.Status != tasks.Failed || v.Problem == nil || v.Problem.Source != "delivery" {
					t.Fatal("expected recoverable collection", v)
				}
				if mode == "commit-restart" {
					h.Close()
					db.Exec("DROP TRIGGER generation_fault")
					h = newHandler("http://tauri.localhost", db, workspace.NewService(db, ws), ps).(*Server)
					if e = h.tasks.Recover(ctx); e != nil {
						t.Fatal(e)
					}
				}
				if failedTarget {
					checkTarget(true)
				}
				if mode == "partial-body" && (v.Problem == nil || !v.Problem.Retryable || strings.Contains(v.Problem.Message, "private-result-token")) {
					t.Fatal("interrupted body lost safe retry or leaked URL", v)
				}
				retry := call("/api/tasks/"+id+"/resolve", map[string]any{"requestId": "collection-retry", "action": "retry", "expected_revision": v.Revision})
				if retry.Code != 200 {
					t.Fatal(retry.Code, retry.Body.String())
				}
				deadline = time.Now().Add(6 * time.Second)
				for time.Now().Before(deadline) {
					v, _ = h.tasks.Query(ctx, project.ID, id)
					if tasks.Terminal(v.Status) {
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
			if mode == "adopt-late" || archiveAdoption || cancelTarget {
				var count int
				db.QueryRow("SELECT count(*) FROM versions").Scan(&count)
				if count != 0 && !archiveAdoption || archiveAdoption && count != 2 {
					t.Fatal("late output auto-delivered")
				}
				deadline = time.Now().Add(4 * time.Second)
				var adoption string
				for time.Now().Before(deadline) {
					db.QueryRow("SELECT id FROM questions WHERE task_id=? AND kind='delivery'", id).Scan(&adoption)
					if adoption != "" {
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
				if adoption == "" {
					t.Fatal("no explicit adoption card for late result")
				}
				if cancelTarget {
					checkTarget(true)
				}
				if archiveAdoption {
					var source, archive, originalInput, originalPlan, status string
					if e = db.QueryRow("SELECT s.source_path,s.archive_path,d.input_json,d.plan_json,d.status FROM delivery_sources s JOIN deliveries d USING(project_id,request_id)").Scan(&source, &archive, &originalInput, &originalPlan, &status); e != nil || status != "blocked" {
						t.Fatal("cancel archive checkpoint", status, e)
					}
					op := strings.TrimPrefix(mode, "adopt-archive-")
					input := map[string]any{"requestId": "mutate-archive", "paths": []string{archive}}
					if op == "rename" {
						input = map[string]any{"requestId": "mutate-archive", "path": archive, "new_name": "moved.png"}
					}
					mutation := call("/api/files/"+op, input)
					if mutation.Code != 409 {
						t.Fatal("retained archive mutation allowed", op, mutation.Code, mutation.Body.String())
					}
					f, err := ws.OpenRegular(project.ID, archive)
					if err != nil {
						t.Fatal("retained archive lost", err)
					}
					f.Close()
					var savedPath, savedInput, savedPlan string
					db.QueryRow("SELECT path FROM task_outputs WHERE task_id=?", id).Scan(&savedPath)
					db.QueryRow("SELECT input_json,plan_json FROM deliveries").Scan(&savedInput, &savedPlan)
					if savedPath != source || savedInput != originalInput || savedPlan != originalPlan {
						t.Fatal("frozen adoption identity changed")
					}
				}
				body := map[string]any{"requestId": "adopt", "expected_revision": 1, "answers": [][]string{{"采用已返回结果"}}}
				adopted := call("/api/question/"+adoption+"/reply", body)
				if adopted.Code != 200 {
					t.Fatal(adopted.Code, adopted.Body.String())
				}
				if adoptionInvalid {
					var state string
					for deadline = time.Now().Add(4 * time.Second); time.Now().Before(deadline); {
						db.QueryRow("SELECT state FROM task_attempts WHERE task_id=?", id).Scan(&state)
						if h.tasks.Active() == 0 {
							break
						}
						time.Sleep(10 * time.Millisecond)
					}
					if state != "failed" {
						t.Error("immutable adoption incorrectly left retry pending", state)
					}
					checkTarget(false)
					db.QueryRow("SELECT count(*) FROM versions").Scan(&count)
					if count != 0 || posts.Load() != 1 {
						t.Fatal("immutable adoption generated/delivered", count, posts.Load())
					}
					return
				}
				deadline = time.Now().Add(4 * time.Second)
				for time.Now().Before(deadline) {
					db.QueryRow("SELECT count(*) FROM versions WHERE id=?", version).Scan(&count)
					if count == 1 {
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
				if count != 1 {
					t.Fatal("adoption did not deliver reserved version")
				}
				v, _ = h.tasks.Query(ctx, project.ID, id)
				if v.Status != tasks.Cancelled {
					t.Fatal("adoption revived cancelled task")
				}
				again := call("/api/question/"+adoption+"/reply", body)
				if again.Code != 200 || again.Body.String() != adopted.Body.String() {
					t.Fatal("adoption receipt replay changed")
				}
				if posts.Load() != 1 {
					t.Fatal("adoption generated again")
				}
				if archiveAdoption {
					db.QueryRow("SELECT count(*) FROM versions").Scan(&count)
					if count != 3 || downloads.Load() != 1 {
						t.Fatal("adoption repeated collection or version insertion", count, downloads.Load())
					}
					checkCurrent()
				}
				if cancelTarget {
					var cid, tid, vid string
					db.QueryRow("SELECT json_extract(source_json,'$.composition_id'),json_extract(source_json,'$.type_id'),version_id FROM task_outputs WHERE task_id=?", id).Scan(&cid, &tid, &vid)
					if vid != version || targetType != "" && tid != targetType || targetComposition != "" && cid != targetComposition {
						t.Fatal("adoption retargeted frozen identity")
					}
					// All outputs delivered: names are ordinary editable objects again.
					targetType, targetComposition = tid, cid
					checkTarget(false)
				}
				return
			}
			want := tasks.Completed
			if mode == "cancel-download" || mode == "cancel-archive" {
				want = tasks.Cancelled
			}
			if mode == "unknown" {
				want = tasks.Unknown
			}
			if mode == "reject" || mode == "invalid-media" || mode == "bad-count" {
				want = tasks.Failed
			}
			if v.Status != want {
				t.Fatalf("status %s want %s: %+v", v.Status, want, v)
			}
			if posts.Load() != 1 {
				t.Fatal("generation was resubmitted", posts.Load())
			}
			if mode == "invalid-media" || mode == "bad-count" {
				if v.Problem == nil || v.Problem.Source != "delivery" || v.Problem.Retryable || len(v.Actions) != 0 {
					t.Fatal("immutable invalid media advertised impossible retry", v)
				}
			}
			if mode == "receipt-restart" {
				h.Close()
				// The committed Delivery callback has completed the task, while
				// worker.finish has not acknowledged its collecting attempt.
				if _, e = db.Exec("UPDATE task_attempts SET state='collecting' WHERE task_id=?", id); e != nil {
					t.Fatal(e)
				}
				h = newHandler("http://tauri.localhost", db, workspace.NewService(db, ws), ps).(*Server)
				if e = h.tasks.Recover(ctx); e != nil {
					t.Fatal(e)
				}
				v, e = h.tasks.Query(ctx, project.ID, id)
				if e != nil || v.Status != tasks.Completed || posts.Load() != 1 || downloads.Load() != 1 {
					t.Fatal("committed receipt recovery repeated work", v, e)
				}
			}

			var count int
			db.QueryRow("SELECT count(*) FROM versions").Scan(&count)
			if want != tasks.Completed {
				if count != 0 {
					t.Fatal("cancelled/unknown/rejected output delivered")
				}
				return
			}
			if count != 1 && mode != "late-version" || mode == "late-version" && count != 3 {
				t.Fatal("duplicate versions", count)
			}
			for _, suffix := range []string{"&offset=0&limit=1", "&offset=1&limit=1"} {
				r := httptest.NewRequest("GET", "/api/tasks?project_id="+project.ID+suffix, nil)
				r.Header.Set("Origin", "http://tauri.localhost")
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				var page struct {
					Items    []tasks.TaskView
					Coverage store.Coverage
				}
				json.Unmarshal(w.Body.Bytes(), &page)
				if w.Code != 200 || page.Coverage.Total != 1 || !page.Coverage.Exhausted {
					t.Fatal("task coverage unavailable", w.Code, w.Body.String())
				}
			}

			var gotResource, path string
			var gotOrdinal int
			if e = db.QueryRow("SELECT v.resource_id,a.path,c.ordinal FROM versions v JOIN assets a ON a.id=v.asset_id JOIN canvas_version_state c ON c.version_id=v.id WHERE v.id=?", version).Scan(&gotResource, &path, &gotOrdinal); e != nil {
				t.Fatal("reserved version lost", e)
			}
			if gotResource != resource || gotOrdinal != ordinal {
				t.Fatal("reserved slot changed")
			}
			if mode == "partial-body" {
				var attempt int
				db.QueryRow("SELECT current_attempt FROM tasks WHERE id=?", id).Scan(&attempt)
				if attempt != 1 || downloads.Load() != 2 {
					t.Fatal("partial-body retry changed attempt/download identity", attempt, downloads.Load())
				}
			}
			checkCurrent()
			if failedTarget {
				var cid, tid string
				db.QueryRow("SELECT json_extract(source_json,'$.composition_id'),json_extract(source_json,'$.type_id') FROM task_outputs WHERE task_id=?", id).Scan(&cid, &tid)
				targetType, targetComposition = tid, cid
				checkTarget(false)
			}
			f, e := ws.OpenRegular(project.ID, path)
			if e != nil {
				t.Fatal(e)
			}
			f.Close()
			if mode == "commit-restart" && downloads.Load() != 1 {
				t.Fatal("archive recovery redownloaded", downloads.Load())
			}
			// A late stale cancel must not overwrite a committed delivery.
			stale := call("/api/tasks/"+id+"/resolve", map[string]any{"requestId": "late-cancel", "action": "cancel", "expected_revision": v.Revision})
			if stale.Code != 409 {
				t.Fatal("completed task cancelled", stale.Code)
			}
		})
	}
}
