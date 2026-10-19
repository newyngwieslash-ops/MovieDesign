package pipeline

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/events"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"strings"
	"time"
)

// Binding is an immutable artifact identity, not an agent approval claim.
type Binding struct {
	PipelineID        string `json:"pipelineId"`
	Gate              string `json:"gate"`
	Revision          int64  `json:"revision"`
	DocumentVersionID string `json:"documentVersionId"`
	SemanticHash      string `json:"semanticHash"`
}
type Approval struct {
	Binding
	ReceiptID string `json:"receiptId"`
}
type HumanReceipt struct {
	ProjectID, QuestionID, ReceiptID string
	Binding                          Binding
}

func ApprovalMatches(current Revision, approved Approval) bool {
	return approved.Gate == "storyboard" && approved.PipelineID == current.PipelineID && approved.Revision == current.Revision && approved.SemanticHash == current.SemanticHash && approved.DocumentVersionID == current.DocumentVersionID && approved.ReceiptID != ""
}

type query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func Current(ctx context.Context, tx query, project, pipeline, gate string) (Binding, error) {
	b := Binding{PipelineID: pipeline, Gate: gate}
	var e error
	if gate == "storyboard" {
		e = tx.QueryRowContext(ctx, `SELECT s.revision,s.document_version_id,s.semantic_hash FROM storyboard_revisions s JOIN pipelines p ON p.id=s.pipeline_id WHERE p.project_id=? AND p.id=? AND s.revision=p.revision`, project, pipeline).Scan(&b.Revision, &b.DocumentVersionID, &b.SemanticHash)
	} else if gate == "intent" || gate == "script" {
		e = tx.QueryRowContext(ctx, `SELECT a.revision,a.document_version_id,a.semantic_hash FROM creative_artifacts a JOIN pipelines p ON p.id=a.pipeline_id WHERE p.project_id=? AND p.id=? AND a.gate=? ORDER BY a.revision DESC LIMIT 1`, project, pipeline, gate).Scan(&b.Revision, &b.DocumentVersionID, &b.SemanticHash)
	} else {
		return b, invalid("Unknown creative gate")
	}
	if errors.Is(e, sql.ErrNoRows) {
		return b, conflict()
	}
	return b, e
}
func ApprovedSource(ctx context.Context, tx query, p, id, gate string) (string, error) {
	if gate == "intent" {
		return "", nil
	}
	upstream := "intent"
	if gate == "storyboard" {
		upstream = "script"
	}
	b, e := Current(ctx, tx, p, id, upstream)
	if e != nil {
		return "", e
	}
	var n int
	e = tx.QueryRowContext(ctx, `SELECT count(*) FROM approvals WHERE pipeline_id=? AND gate=? AND revision=? AND document_version_id=? AND semantic_hash=? AND valid=1`, id, upstream, b.Revision, b.DocumentVersionID, b.SemanticHash).Scan(&n)
	if e != nil {
		return "", e
	}
	if n == 0 {
		return "", &store.DomainError{Status: 409, Code: "waiting_user", Message: "Current " + upstream + " artifact requires human approval"}
	}
	// A script may only authorize its originally frozen current intent.
	if upstream == "script" {
		source, e := ApprovedSource(ctx, tx, p, id, "script")
		if e != nil {
			return "", e
		}
		var actual string
		if e = tx.QueryRowContext(ctx, "SELECT source_version_id FROM creative_artifacts WHERE document_version_id=?", b.DocumentVersionID).Scan(&actual); e != nil {
			return "", e
		}
		if source != actual {
			return "", conflict()
		}
	}
	return b.DocumentVersionID, nil
}
func CheckBinding(ctx context.Context, tx query, p string, b Binding) error {
	current, e := Current(ctx, tx, p, b.PipelineID, b.Gate)
	if e != nil {
		return e
	}
	if current != b {
		return conflict()
	}
	source, e := ApprovedSource(ctx, tx, p, b.PipelineID, b.Gate)
	if e != nil {
		return e
	}
	if source != "" {
		var n int
		e = tx.QueryRowContext(ctx, "SELECT count(*) FROM version_dependencies WHERE version_id=? AND source_version_id=? AND kind='creative_source'", b.DocumentVersionID, source).Scan(&n)
		if e != nil {
			return e
		}
		if n != 1 {
			return conflict()
		}
	}
	return nil
}

// ApproveStoryboard consumes an already inserted human receipt in the caller's
// transaction. It never trusts a native event or an arbitrary receipt identifier.
func ApproveStoryboard(ctx context.Context, tx *sql.Tx, h HumanReceipt) (Approval, error) {
	b := h.Binding
	a := Approval{Binding: b, ReceiptID: h.ReceiptID}
	if e := CheckBinding(ctx, tx, h.ProjectID, b); e != nil {
		return Approval{}, e
	}
	var n int
	encoded, _ := json.Marshal(b)
	e := tx.QueryRowContext(ctx, `SELECT count(*) FROM question_receipts r JOIN questions q ON q.id=r.question_id WHERE r.id=? AND r.question_id=? AND r.actor='human' AND q.project_id=? AND q.kind='creative' AND q.state='approved' AND json_extract(q.question_json,'$.creative')=json(?) AND json_extract(r.response_json,'$.state')='approved'`, h.ReceiptID, h.QuestionID, h.ProjectID, string(encoded)).Scan(&n)
	if e != nil {
		return Approval{}, e
	}
	if n != 1 {
		return Approval{}, invalid("A bound human question receipt is required")
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO approvals(id,pipeline_id,revision,gate,receipt_id,document_version_id,semantic_hash) VALUES(?,?,?,?,?,?,?)`, uuid.NewString(), b.PipelineID, b.Revision, b.Gate, h.ReceiptID, b.DocumentVersionID, b.SemanticHash)
	return a, e
}

// Invalidate preserves receipt history and submitted contracts/frozen tasks.
func Invalidate(ctx context.Context, tx *sql.Tx, p, id, gate string) error {
	gates := []string{"storyboard"}
	if gate == "script" {
		gates = []string{"script", "storyboard"}
	}
	if gate == "intent" {
		gates = []string{"intent", "script", "storyboard"}
	}
	for _, g := range gates {
		if _, e := tx.ExecContext(ctx, "UPDATE approvals SET valid=0 WHERE pipeline_id=? AND gate=?", id, g); e != nil {
			return e
		}
		rows, e := tx.QueryContext(ctx, `SELECT id,question_json FROM questions WHERE project_id=? AND kind='creative' AND state IN ('pending','approved') AND json_extract(question_json,'$.creative.pipelineId')=? AND json_extract(question_json,'$.creative.gate')=?`, p, id, g)
		if e != nil {
			return e
		}
		type card struct{ id, raw string }
		cards := []card{}
		for rows.Next() {
			var c card
			if e = rows.Scan(&c.id, &c.raw); e != nil {
				rows.Close()
				return e
			}
			cards = append(cards, c)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, c := range cards {
			var q map[string]any
			if e = json.Unmarshal([]byte(c.raw), &q); e != nil {
				return e
			}
			q["state"] = "superseded"
			q["revision"] = q["revision"].(float64) + 1
			q["reason"] = "创作内容已变化；旧批准不能授权当前版本。已提交任务保留原版本，请明确决定是否取消。"
			raw, _ := json.Marshal(q)
			if _, e = tx.ExecContext(ctx, "UPDATE questions SET state='superseded',revision=revision+1,question_json=? WHERE id=?", string(raw), c.id); e != nil {
				return e
			}
			session, _ := q["session_id"].(string)
			if e = events.Append(ctx, tx, p, session, "", "question_resolved", raw); e != nil {
				return e
			}
		}
	}
	if _, e := tx.ExecContext(ctx, "UPDATE prompt_contracts SET status='invalidated' WHERE pipeline_id=? AND status IN ('pending','ready')", id); e != nil {
		return e
	}
	_, e := tx.ExecContext(ctx, "UPDATE pipelines SET prompt_status='pending' WHERE id=? AND project_id=?", id, p)
	return e
}
func contentHash(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}
func checkArtifact(ctx context.Context, tx *sql.Tx, p string, d Document, base string) (string, error) {
	var project string
	e := tx.QueryRowContext(ctx, "SELECT project_id FROM pipelines WHERE id=?", d.PipelineID).Scan(&project)
	if errors.Is(e, sql.ErrNoRows) {
		if d.Kind == "intent" && d.ExpectedRevision == 0 && base == "" {
			return "", nil
		}
		return "", conflict()
	}
	if e != nil {
		return "", e
	}
	if project != p {
		return "", conflict()
	}
	source, e := ApprovedSource(ctx, tx, p, d.PipelineID, d.Kind)
	if e != nil {
		return "", e
	}
	if d.SourceVersionID != "" && source != d.SourceVersionID {
		return "", conflict()
	}
	b, e := Current(ctx, tx, p, d.PipelineID, d.Kind)
	if e != nil {
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM creative_artifacts WHERE pipeline_id=? AND gate=?", d.PipelineID, d.Kind).Scan(&n); err != nil {
			return "", err
		}
		if n == 0 && d.ExpectedRevision == 0 && base == "" {
			return "", nil
		}
		return "", e
	}
	if b.Revision != d.ExpectedRevision || base != b.DocumentVersionID {
		return "", conflict()
	}
	var oldSource sql.NullString
	if e = tx.QueryRowContext(ctx, "SELECT source_version_id FROM creative_artifacts WHERE document_version_id=?", base).Scan(&oldSource); e != nil {
		return "", e
	}
	if b.SemanticHash == contentHash(d.Content) && oldSource.String == source {
		return base, nil
	}
	return "", nil
}
func SaveArtifact(ctx context.Context, tx *sql.Tx, p string, d Document, base, resource, version string) error {
	if _, e := Check(ctx, tx, p, d, base); e != nil {
		return e
	}
	if _, e := tx.ExecContext(ctx, "INSERT INTO pipelines(id,project_id) VALUES(?,?) ON CONFLICT(id) DO NOTHING", d.PipelineID, p); e != nil {
		return e
	}
	if d.ExpectedRevision > 0 {
		var r string
		if e := tx.QueryRowContext(ctx, "SELECT resource_id FROM creative_artifacts WHERE pipeline_id=? AND gate=? AND revision=?", d.PipelineID, d.Kind, d.ExpectedRevision).Scan(&r); e != nil {
			return e
		}
		if r != resource {
			return conflict()
		}
	}
	var duplicate int
	if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM creative_artifacts WHERE resource_id=? AND (pipeline_id<>? OR gate<>?)`, resource, d.PipelineID, d.Kind).Scan(&duplicate); e != nil {
		return e
	}
	if duplicate != 0 {
		return invalid("Independent artifacts require independent Resources")
	}
	var source any
	if d.SourceVersionID != "" {
		source = d.SourceVersionID
	}
	_, e := tx.ExecContext(ctx, "INSERT INTO creative_artifacts(pipeline_id,gate,revision,document_version_id,resource_id,content,semantic_hash,source_version_id,created_at) VALUES(?,?,?,?,?,?,?,?,?)", d.PipelineID, d.Kind, d.ExpectedRevision+1, version, resource, d.Content, contentHash(d.Content), source, time.Now().UnixMilli())
	if e != nil {
		return e
	}
	if d.SourceVersionID != "" {
		if _, e = tx.ExecContext(ctx, "INSERT INTO version_dependencies(version_id,source_version_id,kind)VALUES(?,?,'creative_source')", version, d.SourceVersionID); e != nil {
			return e
		}
	}
	return Invalidate(ctx, tx, p, d.PipelineID, d.Kind)
}

func (d Document) Render() string {
	if d.Kind == "storyboard" {
		return d.Storyboard.Render()
	}
	return "# " + d.Kind + "\n\n" + d.Content
}
func (d Document) ValidateContent() error {
	if strings.TrimSpace(d.Content) == "" || len(d.Content) > 1<<20 {
		return invalid("Creative artifact requires complete content")
	}
	return nil
}
func (d Document) MarshalJSON() ([]byte, error) {
	type plain Document
	b, e := json.Marshal(plain(d))
	if e != nil {
		return nil, e
	}
	if d.Kind == "storyboard" {
		return b, nil
	}
	var fields map[string]json.RawMessage
	if e = json.Unmarshal(b, &fields); e != nil {
		return nil, e
	}
	delete(fields, "storyboard")
	return json.Marshal(fields)
}
func BindingForVersion(ctx context.Context, tx query, p, version string) (Binding, error) {
	var b Binding
	e := tx.QueryRowContext(ctx, `SELECT a.pipeline_id,a.gate,a.revision,a.document_version_id,a.semantic_hash FROM creative_artifacts a JOIN pipelines p ON p.id=a.pipeline_id WHERE p.project_id=? AND a.document_version_id=? UNION ALL SELECT s.pipeline_id,'storyboard',s.revision,s.document_version_id,s.semantic_hash FROM storyboard_revisions s JOIN pipelines p ON p.id=s.pipeline_id WHERE p.project_id=? AND s.document_version_id=?`, p, version, p, version).Scan(&b.PipelineID, &b.Gate, &b.Revision, &b.DocumentVersionID, &b.SemanticHash)
	if errors.Is(e, sql.ErrNoRows) {
		return b, &store.DomainError{Status: 404, Code: "not_found", Message: "Creative artifact not found"}
	}
	return b, e
}
func Preview(ctx context.Context, tx query, b Binding) (string, error) {
	if b.Gate == "storyboard" {
		var raw string
		e := tx.QueryRowContext(ctx, "SELECT content_json FROM storyboard_revisions WHERE pipeline_id=? AND revision=? AND document_version_id=?", b.PipelineID, b.Revision, b.DocumentVersionID).Scan(&raw)
		if e != nil {
			return "", e
		}
		var board Storyboard
		if e = json.Unmarshal([]byte(raw), &board); e != nil {
			return "", e
		}
		return board.Render(), nil
	}
	var content string
	e := tx.QueryRowContext(ctx, "SELECT content FROM creative_artifacts WHERE pipeline_id=? AND gate=? AND revision=? AND document_version_id=?", b.PipelineID, b.Gate, b.Revision, b.DocumentVersionID).Scan(&content)
	return (Document{Kind: b.Gate, Content: content}).Render(), e
}
