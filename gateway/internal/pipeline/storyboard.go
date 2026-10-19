package pipeline

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"sort"
	"strings"
	"time"
)

// MovieDesign typed port: semantic timepoints, continuous narrative chapters,
// and one render plan. Shots are not independently generated clips.
type Panel struct {
	ID         string `json:"id"`
	TimeMS     int64  `json:"timeMs"`
	Framing    string `json:"framing"`
	Action     string `json:"action"`
	Transition string `json:"transition"`
}
type Shot struct {
	ID            string   `json:"id"`
	StartMS       int64    `json:"startMs"`
	EndMS         int64    `json:"endMs"`
	PanelIDs      []string `json:"panelIds"`
	NarrativeRole string   `json:"narrativeRole"`
	StartState    string   `json:"startState"`
	EndState      string   `json:"endState"`
}
type Timeline struct {
	DurationMS  int64    `json:"durationMs"`
	ShotIDs     []string `json:"shotIds"`
	AudioIntent string   `json:"audioIntent"`
}
type Storyboard struct {
	Panels   []Panel  `json:"panels"`
	Shots    []Shot   `json:"shots"`
	Timeline Timeline `json:"timeline"`
}
type Document struct {
	Kind             string         `json:"kind"`
	PipelineID       string         `json:"pipelineId"`
	ExpectedRevision int64          `json:"expectedRevision"`
	Storyboard       Storyboard     `json:"storyboard,omitempty"`
	Content          string         `json:"content,omitempty"`
	SourceVersionID  string         `json:"sourceVersionId,omitempty"`
	Contract         *ContractDraft `json:"contract,omitempty"`
}
type Revision struct {
	PipelineID        string     `json:"pipelineId"`
	Revision          int64      `json:"revision"`
	Storyboard        Storyboard `json:"storyboard"`
	SemanticHash      string     `json:"semanticHash"`
	DocumentVersionID string     `json:"documentVersionId"`
	CreatedAt         int64      `json:"createdAt"`
	CurrentRevision   int64      `json:"currentRevision"`
	PromptStatus      string     `json:"promptStatus"`
	ApprovalStatus    string     `json:"approvalStatus"`
}

func required(raw []byte, names ...string) error {
	var fields map[string]json.RawMessage
	if e := json.Unmarshal(raw, &fields); e != nil {
		return e
	}
	for _, name := range names {
		if len(fields[name]) == 0 || string(fields[name]) == "null" {
			return invalid("Required storyboard field: " + name)
		}
	}
	return nil
}
func (d *Document) UnmarshalJSON(raw []byte) error {
	type plain Document
	if e := required(raw, "kind", "pipelineId", "expectedRevision"); e != nil {
		return e
	}
	v, e := store.DecodeValue[plain](raw)
	*d = Document(v)
	if e != nil {
		return e
	}
	if d.Kind == "storyboard" {
		return required(raw, "storyboard")
	}
	return required(raw, "content")
}
func (p *Panel) UnmarshalJSON(raw []byte) error {
	type plain Panel
	if e := required(raw, "id", "timeMs", "framing", "action", "transition"); e != nil {
		return e
	}
	v, e := store.DecodeValue[plain](raw)
	*p = Panel(v)
	return e
}
func (s *Shot) UnmarshalJSON(raw []byte) error {
	type plain Shot
	if e := required(raw, "id", "startMs", "endMs", "panelIds", "narrativeRole", "startState", "endState"); e != nil {
		return e
	}
	v, e := store.DecodeValue[plain](raw)
	*s = Shot(v)
	return e
}
func (t *Timeline) UnmarshalJSON(raw []byte) error {
	type plain Timeline
	if e := required(raw, "durationMs", "shotIds", "audioIntent"); e != nil {
		return e
	}
	v, e := store.DecodeValue[plain](raw)
	*t = Timeline(v)
	return e
}

func invalid(message string) error {
	return &store.DomainError{Status: 400, Code: "invalid_input", Message: message}
}
func conflict() error {
	return &store.DomainError{Status: 409, Code: "invalid_input", Message: "Storyboard revision changed; reload and submit a new requestId"}
}
func (d Document) Validate() error {
	if (d.Kind != "storyboard" && d.Kind != "intent" && d.Kind != "script" && d.Kind != "prompt") || d.ExpectedRevision < 0 || (store.Scope{ProjectID: d.PipelineID, Operation: "storyboard", RequestID: "validate"}).Validate() != nil {
		return invalid("Invalid typed storyboard identity")
	}
	if d.Kind != "prompt" && d.Contract != nil {
		return invalid("Contract draft is only supported for prompt documents")
	}
	if d.Kind != "storyboard" {
		if len(d.Storyboard.Panels) != 0 || len(d.Storyboard.Shots) != 0 || d.Storyboard.Timeline.DurationMS != 0 {
			return invalid("Upstream artifact cannot contain storyboard")
		}
		if d.Kind == "intent" && d.SourceVersionID != "" {
			return invalid("Intent has no upstream source")
		}
		if d.Kind == "prompt" {
			return validatePromptContent(d.Content)
		}
		return d.ValidateContent()
	}
	if d.Content != "" {
		return invalid("Storyboard uses structured content")
	}
	return d.Storyboard.Validate()
}
func (s Storyboard) Validate() error {
	if len(s.Panels) == 0 || len(s.Shots) == 0 || strings.TrimSpace(s.Timeline.AudioIntent) == "" || len(s.Timeline.ShotIDs) != len(s.Shots) || s.Timeline.DurationMS <= 0 || s.Timeline.DurationMS > 9007199254740991 {
		return invalid("Storyboard requires panels, shots and a complete integer-millisecond timeline")
	}
	ids := map[string]bool{}
	panels := map[string]Panel{}
	shots := map[string]Shot{}
	used := map[string]bool{}
	unique := func(id string) bool {
		if id == "" || len(id) > 128 || ids[id] {
			return false
		}
		ids[id] = true
		return true
	}
	for _, p := range s.Panels {
		if !unique(p.ID) || strings.TrimSpace(p.Framing) == "" || strings.TrimSpace(p.Action) == "" || strings.TrimSpace(p.Transition) == "" || p.TimeMS < 0 || p.TimeMS > s.Timeline.DurationMS {
			return invalid("Invalid or duplicate Panel")
		}
		panels[p.ID] = p
	}
	for _, sh := range s.Shots {
		if !unique(sh.ID) || strings.TrimSpace(sh.NarrativeRole) == "" || strings.TrimSpace(sh.StartState) == "" || strings.TrimSpace(sh.EndState) == "" || sh.StartMS < 0 || sh.EndMS <= sh.StartMS || sh.EndMS > s.Timeline.DurationMS || len(sh.PanelIDs) == 0 {
			return invalid("Invalid Shot interval or identity")
		}
		seen := map[string]bool{}
		for _, id := range sh.PanelIDs {
			p, ok := panels[id]
			if !ok || seen[id] || p.TimeMS < sh.StartMS || p.TimeMS > sh.EndMS {
				return invalid("Shot references a missing, repeated or out-of-range Panel")
			}
			seen[id] = true
			used[id] = true
		}
		shots[sh.ID] = sh
	}
	if len(used) != len(panels) {
		return invalid("Every Panel must belong to a logical Shot")
	}
	seen := map[string]bool{}
	intervals := []Shot{}
	for _, id := range s.Timeline.ShotIDs {
		sh, ok := shots[id]
		if !ok || seen[id] {
			return invalid("Timeline must reference every Shot exactly once")
		}
		seen[id] = true
		intervals = append(intervals, sh)
	}
	sort.Slice(intervals, func(i, j int) bool { return intervals[i].StartMS < intervals[j].StartMS })
	end := int64(0)
	for _, sh := range intervals {
		if sh.StartMS > end {
			return invalid("Timeline has a coverage gap")
		}
		end = max(end, sh.EndMS)
	}
	if end != s.Timeline.DurationMS {
		return invalid("Timeline duration disagrees with Shot coverage")
	}
	return nil
}
func (s Storyboard) encoded() ([]byte, string) {
	b, _ := json.Marshal(s)
	h := sha256.Sum256(b)
	return b, hex.EncodeToString(h[:])
}
func (s Storyboard) Render() string {
	b, _ := json.MarshalIndent(s, "", "  ")
	return "# storyboard_review.md\n\n语义分镜候选；文字不代表人类批准。时间单位：整数毫秒。\n\n编辑下方 JSON 后提交结构化校验。\n\n```json\n" + string(b) + "\n```\n"
}
func ParseMarkdown(content string) (Storyboard, error) {
	start := strings.Index(content, "```json\n")
	if start < 0 {
		return Storyboard{}, invalid("Storyboard Markdown requires one json code block")
	}
	tail := content[start+8:]
	end := strings.Index(tail, "\n```")
	if end < 0 || strings.Contains(tail[end+4:], "```json") {
		return Storyboard{}, invalid("Storyboard Markdown requires one complete json code block")
	}
	s, e := store.DecodeValue[Storyboard]([]byte(tail[:end]))
	if e != nil {
		return s, invalid("Invalid storyboard JSON")
	}
	return s, s.Validate()
}

// Check uses only the caller's transaction. It returns the existing Version for
// a semantic no-op; an expected revision is still mandatory for a new intent.
func Check(ctx context.Context, tx *sql.Tx, p string, d Document, base string) (string, error) {
	if e := d.Validate(); e != nil {
		return "", e
	}
	if d.Kind == "prompt" {
		return checkPrompt(ctx, tx, p, d, base)
	}
	if d.Kind != "storyboard" {
		return checkArtifact(ctx, tx, p, d, base)
	}
	source, e := ApprovedSource(ctx, tx, p, d.PipelineID, "storyboard")
	if e != nil {
		return "", e
	}
	if d.SourceVersionID != "" && source != d.SourceVersionID {
		return "", conflict()
	}
	var project string
	var current int64
	e = tx.QueryRowContext(ctx, "SELECT project_id,revision FROM pipelines WHERE id=?", d.PipelineID).Scan(&project, &current)
	if errors.Is(e, sql.ErrNoRows) {
		if d.ExpectedRevision != 0 || base != "" {
			return "", conflict()
		}
		return "", nil
	}
	if e != nil {
		return "", e
	}
	if project != p || current != d.ExpectedRevision {
		return "", conflict()
	}
	if current == 0 {
		if base != "" {
			return "", conflict()
		}
		return "", nil
	}
	var hash, version string
	e = tx.QueryRowContext(ctx, "SELECT semantic_hash,document_version_id FROM storyboard_revisions WHERE pipeline_id=? AND revision=?", d.PipelineID, current).Scan(&hash, &version)
	if e != nil {
		return "", e
	}
	if base != version {
		return "", conflict()
	}
	_, h := d.Storyboard.encoded()
	var dependencies int
	if e = tx.QueryRowContext(ctx, "SELECT count(*) FROM version_dependencies WHERE version_id=? AND source_version_id=? AND kind='creative_source'", version, source).Scan(&dependencies); e != nil {
		return "", e
	}
	if h == hash && dependencies == 1 {
		return version, nil
	}
	return "", nil
}

// SaveStoryboard is the supplied-Tx commit seam of shared document_write / Delivery.
// The actual resource/version already exist in this same transaction; no I/O or
// reentrant DB calls are allowed here. Delivery owns original request/replay.
func SaveStoryboard(ctx context.Context, tx *sql.Tx, scope store.Scope, requestID, pipelineID string, expectedRevision int64, s Storyboard, base, resource, version string) (Revision, error) {
	d := Document{Kind: "storyboard", PipelineID: pipelineID, ExpectedRevision: expectedRevision, Storyboard: s}
	if _, e := Check(ctx, tx, scope.ProjectID, d, base); e != nil {
		return Revision{}, e
	}
	b, h := s.encoded()
	now := time.Now().UnixMilli()
	revision := expectedRevision + 1
	if expectedRevision == 0 {
		if _, e := tx.ExecContext(ctx, "UPDATE pipelines SET resource_id=?3 WHERE id=?1 AND project_id=?2 AND resource_id IS NULL", pipelineID, scope.ProjectID, resource); e != nil {
			return Revision{}, e
		}
	}
	res, e := tx.ExecContext(ctx, "UPDATE pipelines SET revision=?,prompt_status='pending' WHERE id=? AND project_id=? AND revision=? AND resource_id=?", revision, pipelineID, scope.ProjectID, expectedRevision, resource)
	if e != nil {
		return Revision{}, e
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return Revision{}, conflict()
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO storyboard_revisions(pipeline_id,revision,content_json,semantic_hash,document_version_id,created_at) VALUES(?,?,?,?,?,?)", pipelineID, revision, string(b), h, version, now); e != nil {
		return Revision{}, e
	}
	if e = Invalidate(ctx, tx, scope.ProjectID, pipelineID, "storyboard"); e != nil {
		return Revision{}, e
	}
	if _, e = tx.ExecContext(ctx, "UPDATE prompt_contracts SET status='invalidated' WHERE pipeline_id=? AND status IN ('pending','ready')", pipelineID); e != nil {
		return Revision{}, e
	}
	source, e := ApprovedSource(ctx, tx, scope.ProjectID, pipelineID, "storyboard")
	if e != nil {
		return Revision{}, e
	}
	if base == "" {
		if _, e = tx.ExecContext(ctx, "UPDATE versions SET source_version_id=? WHERE id=?", source, version); e != nil {
			return Revision{}, e
		}
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO version_dependencies(version_id,source_version_id,kind) VALUES(?,?,'creative_source')", version, source); e != nil {
		return Revision{}, e
	}
	if base != "" {
		if _, e = tx.ExecContext(ctx, "INSERT INTO version_dependencies(version_id,source_version_id,kind) VALUES(?,?,'storyboard_revision')", version, base); e != nil {
			return Revision{}, e
		}
	}
	return Revision{pipelineID, revision, s, h, version, now, revision, "pending", "unapproved"}, nil
}
func ReadVersion(ctx context.Context, db *sql.DB, p, version string) (Revision, error) {
	var r Revision
	var content string
	e := db.QueryRowContext(ctx, `SELECT s.pipeline_id,s.revision,s.content_json,s.semantic_hash,s.document_version_id,s.created_at,p.revision,p.prompt_status FROM storyboard_revisions s JOIN pipelines p ON p.id=s.pipeline_id WHERE p.project_id=? AND s.document_version_id=?`, p, version).Scan(&r.PipelineID, &r.Revision, &content, &r.SemanticHash, &r.DocumentVersionID, &r.CreatedAt, &r.CurrentRevision, &r.PromptStatus)
	if errors.Is(e, sql.ErrNoRows) {
		return r, &store.DomainError{Status: 404, Code: "not_found", Message: "Storyboard revision not found"}
	}
	if e != nil {
		return r, e
	}
	if e = json.Unmarshal([]byte(content), &r.Storyboard); e != nil {
		return r, fmt.Errorf("stored storyboard: %w", e)
	}
	r.ApprovalStatus = "unapproved"
	var approved int
	if e = db.QueryRowContext(ctx, "SELECT count(*) FROM approvals WHERE pipeline_id=? AND gate='storyboard' AND revision=? AND document_version_id=? AND semantic_hash=? AND valid=1", r.PipelineID, r.Revision, version, r.SemanticHash).Scan(&approved); e != nil {
		return r, e
	}
	if approved > 0 && r.CurrentRevision == r.Revision {
		r.ApprovalStatus = "approved"
	}
	return r, nil
}
