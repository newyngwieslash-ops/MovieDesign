package pipeline

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
)

// PromptDigest computes the exact SHA-256 hex digest of raw prompt bytes without
// normalizing line endings (\n vs \r\n) or trimming leading/trailing whitespace.
func PromptDigest(prompt string) string {
	sum := sha256.Sum256([]byte(prompt))
	return hex.EncodeToString(sum[:])
}

type AudioRequirement struct {
	AudioIntent       string `json:"audioIntent"`
	RequiresAudio     bool   `json:"requiresAudio"`
	RequiresNarration bool   `json:"requiresNarration"`
	RequiresMusic     bool   `json:"requiresMusic"`
	Mode              string `json:"mode,omitempty"`
}

type PromptReference struct {
	Ordinal   int    `json:"ordinal"`
	AssetID   string `json:"assetId"`
	VersionID string `json:"versionId,omitempty"`
	Role      string `json:"role"`
	MediaKind string `json:"mediaKind,omitempty"`
	MIMEType  string `json:"mimeType,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
}

type ContractDraft struct {
	CapabilityID string            `json:"capabilityId"`
	Params       map[string]any    `json:"params,omitempty"`
	References   []string          `json:"references,omitempty"`
	Placeholders []string          `json:"placeholders,omitempty"`
	Audio        *AudioRequirement `json:"audio,omitempty"`
}

type PromptContract struct {
	ID                  string            `json:"id"`
	ProjectID           string            `json:"projectId"`
	PipelineID          string            `json:"pipelineId"`
	Revision            int64             `json:"revision"`
	PromptRevision      int64             `json:"promptRevision"`
	Status              string            `json:"status"`
	PromptResourceID    string            `json:"promptResourceId"`
	PromptVersionID     string            `json:"promptVersionId"`
	PromptAssetID       string            `json:"promptAssetId"`
	PromptFileName      string            `json:"promptFileName"`
	Prompt              string            `json:"prompt"`
	PromptSHA256        string            `json:"promptSha256"`
	PromptBytes         int               `json:"promptBytes"`
	StoryboardVersionID string            `json:"storyboardVersionId"`
	StoryboardRevision  int64             `json:"storyboardRevision"`
	StoryboardHash      string            `json:"storyboardSemanticHash"`
	ApprovalID          string            `json:"approvalId"`
	QuestionReceiptID   string            `json:"questionReceiptId"`
	IntentVersionID     string            `json:"intentVersionId"`
	ScriptVersionID     string            `json:"scriptVersionId"`
	CapabilityID        string            `json:"capabilityId"`
	ProviderID          string            `json:"providerId"`
	ModelID             string            `json:"modelId"`
	DurationMS          int64             `json:"durationMs"`
	DurationSeconds     int               `json:"durationSeconds"`
	AspectRatio         string            `json:"aspectRatio,omitempty"`
	Size                string            `json:"size,omitempty"`
	Count               int               `json:"count"`
	Params              map[string]any    `json:"params"`
	References          []PromptReference `json:"references"`
	Placeholders        []string          `json:"placeholders"`
	Audio               AudioRequirement  `json:"audio"`
	Preflight           PreflightReport   `json:"preflight"`
	TaskID              string            `json:"taskId,omitempty"`
	Recovery            *RecoveryLink     `json:"recovery,omitempty"`
	CreatedAt           int64             `json:"createdAt"`
}

type PromptVersionView struct {
	PipelineID         string          `json:"pipelineId"`
	Revision           int64           `json:"revision"`
	CurrentRevision    int64           `json:"currentRevision"`
	StoryboardRevision int64           `json:"storyboardRevision"`
	CurrentStoryboard  int64           `json:"currentStoryboardRevision"`
	DocumentVersionID  string          `json:"documentVersionId"`
	ResourceID         string          `json:"resourceId"`
	SourceVersionID    string          `json:"sourceVersionId"`
	ApprovalID         string          `json:"approvalId"`
	Content            string          `json:"content"`
	PromptSHA256       string          `json:"promptSha256"`
	PromptBytes        int             `json:"promptBytes"`
	PromptStatus       string          `json:"promptStatus"`
	ApprovalStatus     string          `json:"approvalStatus"`
	CreatedAt          int64           `json:"createdAt"`
	Contract           *PromptContract `json:"contract,omitempty"`
	Lineage            *Lineage        `json:"lineage,omitempty"`
	Recovery           *RecoveryState  `json:"recovery,omitempty"`
}

func validatePromptContent(content string) error {
	if content == "" || strings.TrimSpace(content) == "" || len(content) > 1<<20 || !utf8.ValidString(content) || strings.ContainsRune(content, 0) {
		return invalid("Prompt artifact requires valid non-empty UTF-8 content")
	}
	return nil
}

func checkPrompt(ctx context.Context, tx *sql.Tx, p string, d Document, base string) (string, error) {
	if e := validatePromptContent(d.Content); e != nil {
		return "", e
	}
	var project string
	e := tx.QueryRowContext(ctx, "SELECT project_id FROM pipelines WHERE id=?", d.PipelineID).Scan(&project)
	if errors.Is(e, sql.ErrNoRows) {
		return "", conflict()
	}
	if e != nil {
		return "", e
	}
	if project != p {
		return "", conflict()
	}
	source, e := ApprovedSource(ctx, tx, p, d.PipelineID, "prompt")
	if e != nil {
		return "", e
	}
	if d.SourceVersionID != "" && source != d.SourceVersionID {
		return "", conflict()
	}
	b, e := Current(ctx, tx, p, d.PipelineID, "prompt")
	if e != nil {
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM prompt_revisions WHERE pipeline_id=?", d.PipelineID).Scan(&n); err != nil {
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
	var oldSource string
	if e = tx.QueryRowContext(ctx, "SELECT source_version_id FROM prompt_revisions WHERE document_version_id=?", base).Scan(&oldSource); e != nil {
		return "", e
	}
	if d.Contract == nil && b.SemanticHash == PromptDigest(d.Content) && oldSource == source {
		return base, nil
	}
	return "", nil
}

// SavePrompt commits final_video_prompt.md in the shared Delivery transaction.
// It binds the exact prompt bytes to the currently approved storyboard revision
// and optionally compiles a ready PromptContract when d.Contract is provided.
func SavePrompt(ctx context.Context, tx *sql.Tx, p string, d Document, base, resource, version string) error {
	if _, e := checkPrompt(ctx, tx, p, d, base); e != nil {
		return e
	}
	source, e := ApprovedSource(ctx, tx, p, d.PipelineID, "prompt")
	if e != nil {
		return e
	}
	sb, e := Current(ctx, tx, p, d.PipelineID, "storyboard")
	if e != nil {
		return e
	}
	var approvalID string
	e = tx.QueryRowContext(ctx, `SELECT id FROM approvals WHERE pipeline_id=? AND gate='storyboard' AND revision=? AND document_version_id=? AND semantic_hash=? AND valid=1 ORDER BY rowid DESC LIMIT 1`,
		d.PipelineID, sb.Revision, sb.DocumentVersionID, sb.SemanticHash).Scan(&approvalID)
	if e != nil {
		return &store.DomainError{Status: 409, Code: "waiting_user", Message: "Current storyboard artifact requires human approval"}
	}
	if d.ExpectedRevision > 0 {
		var r string
		if e := tx.QueryRowContext(ctx, "SELECT resource_id FROM prompt_revisions WHERE pipeline_id=? AND revision=?", d.PipelineID, d.ExpectedRevision).Scan(&r); e != nil {
			return e
		}
		if r != resource {
			return conflict()
		}
	}
	var duplicate int
	if e := tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM creative_artifacts WHERE resource_id=?) + (SELECT count(*) FROM pipelines WHERE resource_id=?) + (SELECT count(*) FROM prompt_revisions WHERE resource_id=? AND pipeline_id<>?)`, resource, resource, resource, d.PipelineID).Scan(&duplicate); e != nil {
		return e
	}
	if duplicate != 0 {
		return invalid("Independent artifacts require independent Resources")
	}
	rev := d.ExpectedRevision + 1
	digest := PromptDigest(d.Content)
	now := time.Now().UnixMilli()
	if _, e := tx.ExecContext(ctx, `INSERT INTO prompt_revisions(pipeline_id,revision,storyboard_revision,document_version_id,resource_id,content,prompt_sha256,source_version_id,approval_id,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		d.PipelineID, rev, sb.Revision, version, resource, d.Content, digest, source, approvalID, now); e != nil {
		return e
	}
	if base == "" {
		if _, e := tx.ExecContext(ctx, `UPDATE versions SET source_version_id=? WHERE id=?`, source, version); e != nil {
			return e
		}
	}
	if _, e := tx.ExecContext(ctx, `INSERT INTO version_dependencies(version_id,source_version_id,kind) VALUES(?,?,'creative_source')`, version, source); e != nil {
		return e
	}
	if base != "" {
		if _, e := tx.ExecContext(ctx, `INSERT INTO version_dependencies(version_id,source_version_id,kind) VALUES(?,?,'prompt_revision')`, version, base); e != nil {
			return e
		}
	}
	if _, e := tx.ExecContext(ctx, `UPDATE prompt_contracts SET status='invalidated' WHERE pipeline_id=? AND status IN ('pending','ready')`, d.PipelineID); e != nil {
		return e
	}
	if _, e := tx.ExecContext(ctx, `UPDATE pipelines SET prompt_status='pending' WHERE id=? AND project_id=?`, d.PipelineID, p); e != nil {
		return e
	}
	if d.Contract != nil {
		_, _, e := compileTx(ctx, tx, p, CompileInput{
			PipelineID:         d.PipelineID,
			PromptVersionID:    version,
			Prompt:             d.Content,
			StoryboardRevision: &sb.Revision,
			CapabilityID:       d.Contract.CapabilityID,
			Params:             d.Contract.Params,
			References:         d.Contract.References,
			Placeholders:       d.Contract.Placeholders,
			Audio:              d.Contract.Audio,
		})
		return e
	}
	pendingJSON, _ := json.Marshal(map[string]any{
		"pipelineId":          d.PipelineID,
		"promptRevision":      rev,
		"storyboardRevision":  sb.Revision,
		"promptVersionId":     version,
		"storyboardVersionId": source,
		"promptSha256":        digest,
		"promptBytes":         len(d.Content),
		"approvalId":          approvalID,
		"status":              "pending",
	})
	_, e = tx.ExecContext(ctx, `INSERT INTO prompt_contracts(id,pipeline_id,revision,document_version_id,content_json,status,prompt_revision,prompt_sha256,approval_id,created_at) VALUES(?,?,?,?,?,'pending',?,?,?,?)`,
		uuid.NewString(), d.PipelineID, sb.Revision, version, string(pendingJSON), rev, digest, approvalID, now)
	return e
}

func ReadPromptVersion(ctx context.Context, db *sql.DB, p, versionID string) (PromptVersionView, error) {
	var v PromptVersionView
	e := db.QueryRowContext(ctx, `SELECT r.pipeline_id,r.revision,r.storyboard_revision,r.document_version_id,r.resource_id,r.source_version_id,r.approval_id,r.content,r.prompt_sha256,r.created_at,p.revision,p.prompt_status
		FROM prompt_revisions r JOIN pipelines p ON p.id=r.pipeline_id
		WHERE p.project_id=? AND r.document_version_id=?`, p, versionID).Scan(
		&v.PipelineID, &v.Revision, &v.StoryboardRevision, &v.DocumentVersionID, &v.ResourceID,
		&v.SourceVersionID, &v.ApprovalID, &v.Content, &v.PromptSHA256, &v.CreatedAt,
		&v.CurrentStoryboard, &v.PromptStatus,
	)
	if errors.Is(e, sql.ErrNoRows) {
		return v, &store.DomainError{Status: 404, Code: "not_found", Message: "Prompt revision not found"}
	}
	if e != nil {
		return v, e
	}
	v.PromptBytes = len(v.Content)
	_ = db.QueryRowContext(ctx, `SELECT coalesce(max(revision),0) FROM prompt_revisions WHERE pipeline_id=?`, v.PipelineID).Scan(&v.CurrentRevision)
	var valid int
	_ = db.QueryRowContext(ctx, `SELECT valid FROM approvals WHERE id=?`, v.ApprovalID).Scan(&valid)
	v.ApprovalStatus = "stale"
	if valid == 1 && v.StoryboardRevision == v.CurrentStoryboard && v.Revision == v.CurrentRevision {
		v.ApprovalStatus = "approved"
	}
	var rawContract, status string
	var taskID sql.NullString
	err := db.QueryRowContext(ctx, `SELECT content_json,status,task_id FROM prompt_contracts WHERE pipeline_id=? AND document_version_id=? ORDER BY created_at DESC, rowid DESC LIMIT 1`, v.PipelineID, versionID).Scan(&rawContract, &status, &taskID)
	if err == nil {
		var c PromptContract
		if json.Unmarshal([]byte(rawContract), &c) == nil && c.ID != "" {
			c.Status = status
			if taskID.Valid {
				c.TaskID = taskID.String
			}
			v.Contract = &c
		}
	}
	if lineage, err := TraceLineage(ctx, db, p, versionID); err == nil {
		v.Lineage = &lineage
	}
	v.Recovery = LatestPipelineRecovery(ctx, db, p, v.PipelineID)
	return v, nil
}
