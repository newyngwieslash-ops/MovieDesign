// Package testsupport contains deterministic fixture setup, never production approval routes.
package testsupport

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/pipeline"
	"testing"
)

// CreativeUpstream creates actual immutable documents via the supplied production
// writer, then explicitly seeds human receipt fixtures. Real human-route coverage
// is separate in pipeline approval tests and the native desktop smoke.
func CreativeUpstream(t *testing.T, db *sql.DB, p, id string, write func([]byte) error) {
	t.Helper()
	for _, gate := range []string{"intent", "script"} {
		raw, _ := json.Marshal(map[string]any{"requestId": "fixture-" + gate, "mode": "create", "format": "md", "typedDocument": map[string]any{"kind": gate, "pipelineId": id, "expectedRevision": 0, "content": "Fixture " + gate}, "output": map[string]any{"resources": []any{map[string]any{"key": gate, "mode": "create", "name": gate}}, "composition": map[string]any{"name": "Upstream", "type": map[string]any{"label": "video"}, "placements": []any{map[string]any{"key": gate, "role": gate}}}}})
		if e := write(raw); e != nil {
			t.Fatal(e)
		}
		b, e := pipeline.Current(context.Background(), db, p, id, gate)
		if e != nil {
			t.Fatal(e)
		}
		q := "fixture-" + gate
		if _, e = db.Exec(`INSERT INTO questions(id,project_id,kind,source,revision,input_hash,state,question_json,created_at)VALUES(?,?,'creative','fixture',1,?,'approved','{}',0)`, q, p, b.SemanticHash); e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(`INSERT INTO question_receipts(id,question_id,request_id,actor,response_json,created_at)VALUES(?,?,?,'human','{}',0)`, q, q, q); e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(`INSERT INTO approvals(id,pipeline_id,revision,gate,receipt_id,document_version_id,semantic_hash)VALUES(?,?,1,?,?,?,?)`, q, id, gate, q, b.DocumentVersionID, b.SemanticHash); e != nil {
			t.Fatal(e)
		}
	}
}
