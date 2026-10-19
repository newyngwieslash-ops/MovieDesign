package pipeline

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
)

type DeliveredVersion struct {
	Key           string
	ResourceID    string
	VersionID     string
	AssetID       string
	BaseVersionID string
}

type LineageNode struct {
	Gate              string `json:"gate"`
	Revision          int64  `json:"revision"`
	ResourceID        string `json:"resourceId"`
	DocumentVersionID string `json:"documentVersionId"`
	AssetID           string `json:"assetId"`
	SourceVersionID   string `json:"sourceVersionId,omitempty"`
	SemanticHash      string `json:"semanticHash"`
	ApprovalID        string `json:"approvalId,omitempty"`
	ReceiptID         string `json:"receiptId,omitempty"`
	Valid             bool   `json:"valid"`
}

type DeliveredMedia struct {
	TaskID          string `json:"taskId"`
	Attempt         int    `json:"attempt"`
	OutputKey       string `json:"outputKey"`
	Role            string `json:"role"`
	ResourceID      string `json:"resourceId"`
	VersionID       string `json:"versionId"`
	AssetID         string `json:"assetId"`
	SourceVersionID string `json:"sourceVersionId"`
}

type VersionDependency struct {
	VersionID       string `json:"versionId"`
	SourceVersionID string `json:"sourceVersionId"`
	Kind            string `json:"kind"`
}

type Lineage struct {
	ProjectID    string              `json:"projectId"`
	PipelineID   string              `json:"pipelineId"`
	Intent       *LineageNode        `json:"intent,omitempty"`
	Script       *LineageNode        `json:"script,omitempty"`
	Storyboard   *LineageNode        `json:"storyboard,omitempty"`
	Prompt       *LineageNode        `json:"prompt,omitempty"`
	Contract     *PromptContract     `json:"contract,omitempty"`
	References   []PromptReference   `json:"references"`
	Outputs      []DeliveredMedia    `json:"outputs"`
	Dependencies []VersionDependency `json:"dependencies"`
}

func RecordTaskDeliveryLineage(ctx context.Context, tx *sql.Tx, projectID, taskID string, delivered []DeliveredVersion) error {
	var rawJSON, promptVersionID string
	err := tx.QueryRowContext(ctx, `SELECT c.content_json,c.document_version_id
		FROM prompt_contracts c JOIN pipelines p ON p.id=c.pipeline_id
		WHERE c.task_id=? AND p.project_id=?`, taskID, projectID).Scan(&rawJSON, &promptVersionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var contract PromptContract
	if err := json.Unmarshal([]byte(rawJSON), &contract); err != nil {
		return err
	}
	if contract.PromptVersionID == "" {
		contract.PromptVersionID = promptVersionID
	}
	for _, dv := range delivered {
		if contract.PromptVersionID != "" && contract.PromptVersionID != dv.VersionID {
			if _, err := tx.ExecContext(ctx, `UPDATE versions SET source_version_id=? WHERE id=?`, contract.PromptVersionID, dv.VersionID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO version_dependencies(version_id,source_version_id,kind) VALUES(?,?,'prompt_contract')`, dv.VersionID, contract.PromptVersionID); err != nil {
				return err
			}
		}
		if contract.StoryboardVersionID != "" && contract.StoryboardVersionID != dv.VersionID {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO version_dependencies(version_id,source_version_id,kind) VALUES(?,?,'storyboard_source')`, dv.VersionID, contract.StoryboardVersionID); err != nil {
				return err
			}
		}
		if dv.BaseVersionID != "" && dv.BaseVersionID != dv.VersionID {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO version_dependencies(version_id,source_version_id,kind) VALUES(?,?,'media_revision')`, dv.VersionID, dv.BaseVersionID); err != nil {
				return err
			}
		}
		for _, ref := range contract.References {
			refVersionID := ref.VersionID
			if refVersionID == "" {
				_ = tx.QueryRowContext(ctx, `SELECT v.id FROM versions v JOIN resources r ON r.id=v.resource_id JOIN compositions c ON c.id=r.composition_id WHERE v.asset_id=? AND c.project_id=? ORDER BY v.created_at DESC, v.id DESC LIMIT 1`, ref.AssetID, projectID).Scan(&refVersionID)
			}
			if refVersionID != "" && refVersionID != dv.VersionID {
				if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO version_dependencies(version_id,source_version_id,kind) VALUES(?,?,?)`, dv.VersionID, refVersionID, fmt.Sprintf("reference:%d", ref.Ordinal)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func loadGateNode(ctx context.Context, db *sql.DB, pipelineID, gate, versionID string, rev int64) *LineageNode {
	node := &LineageNode{Gate: gate}
	var src sql.NullString
	var err error
	if gate == "storyboard" {
		q := `SELECT s.revision,p.resource_id,s.document_version_id,v.asset_id,coalesce((SELECT source_version_id FROM version_dependencies WHERE version_id=s.document_version_id AND kind='creative_source' LIMIT 1),v.source_version_id),s.semantic_hash
			FROM storyboard_revisions s JOIN pipelines p ON p.id=s.pipeline_id JOIN versions v ON v.id=s.document_version_id
			WHERE s.pipeline_id=?`
		args := []any{pipelineID}
		if versionID != "" {
			q += ` AND s.document_version_id=?`
			args = append(args, versionID)
		} else if rev > 0 {
			q += ` AND s.revision=?`
			args = append(args, rev)
		} else {
			q += ` ORDER BY s.revision DESC LIMIT 1`
		}
		err = db.QueryRowContext(ctx, q, args...).Scan(&node.Revision, &node.ResourceID, &node.DocumentVersionID, &node.AssetID, &src, &node.SemanticHash)
	} else if gate == "prompt" {
		q := `SELECT r.revision,r.resource_id,r.document_version_id,v.asset_id,r.source_version_id,r.prompt_sha256,r.approval_id
			FROM prompt_revisions r JOIN versions v ON v.id=r.document_version_id
			WHERE r.pipeline_id=?`
		args := []any{pipelineID}
		if versionID != "" {
			q += ` AND r.document_version_id=?`
			args = append(args, versionID)
		} else if rev > 0 {
			q += ` AND r.revision=?`
			args = append(args, rev)
		} else {
			q += ` ORDER BY r.revision DESC LIMIT 1`
		}
		err = db.QueryRowContext(ctx, q, args...).Scan(&node.Revision, &node.ResourceID, &node.DocumentVersionID, &node.AssetID, &src, &node.SemanticHash, &node.ApprovalID)
		if err == nil && node.ApprovalID != "" {
			var valid int
			_ = db.QueryRowContext(ctx, `SELECT receipt_id,valid FROM approvals WHERE id=?`, node.ApprovalID).Scan(&node.ReceiptID, &valid)
			node.Valid = valid == 1
			node.SourceVersionID = src.String
			return node
		}
	} else {
		q := `SELECT a.revision,a.resource_id,a.document_version_id,v.asset_id,coalesce((SELECT source_version_id FROM version_dependencies WHERE version_id=a.document_version_id AND kind='creative_source' LIMIT 1),v.source_version_id),a.semantic_hash
			FROM creative_artifacts a JOIN versions v ON v.id=a.document_version_id
			WHERE a.pipeline_id=? AND a.gate=?`
		args := []any{pipelineID, gate}
		if versionID != "" {
			q += ` AND a.document_version_id=?`
			args = append(args, versionID)
		} else if rev > 0 {
			q += ` AND a.revision=?`
			args = append(args, rev)
		} else {
			q += ` ORDER BY a.revision DESC LIMIT 1`
		}
		err = db.QueryRowContext(ctx, q, args...).Scan(&node.Revision, &node.ResourceID, &node.DocumentVersionID, &node.AssetID, &src, &node.SemanticHash)
	}
	if err != nil {
		return nil
	}
	node.SourceVersionID = src.String
	var valid int
	if db.QueryRowContext(ctx, `SELECT id,receipt_id,valid FROM approvals WHERE pipeline_id=? AND gate=? AND revision=? AND document_version_id=? ORDER BY valid DESC, rowid DESC LIMIT 1`,
		pipelineID, gate, node.Revision, node.DocumentVersionID).Scan(&node.ApprovalID, &node.ReceiptID, &valid) == nil {
		node.Valid = valid == 1
	}
	return node
}

func TraceLineage(ctx context.Context, db *sql.DB, projectID, targetID string) (Lineage, error) {
	out := Lineage{
		ProjectID:    projectID,
		References:   []PromptReference{},
		Outputs:      []DeliveredMedia{},
		Dependencies: []VersionDependency{},
	}
	var pipelineID, contractID, promptVersionID, sbVersionID, scriptVersionID, intentVersionID string

	// Resolve targetID across delivered media versions, prompt contracts, tasks, prompt revisions, storyboard revisions, creative artifacts, or pipelines.
	_ = db.QueryRowContext(ctx, `SELECT c.pipeline_id,c.id,c.document_version_id
		FROM task_outputs o
		JOIN tasks t ON t.id=o.task_id
		JOIN prompt_contracts c ON c.task_id=t.id
		JOIN pipelines p ON p.id=c.pipeline_id
		WHERE p.project_id=? AND (o.version_id=? OR t.id=?)
		ORDER BY o.attempt DESC LIMIT 1`, projectID, targetID, targetID).Scan(&pipelineID, &contractID, &promptVersionID)
	if pipelineID == "" {
		_ = db.QueryRowContext(ctx, `SELECT c.pipeline_id,c.id,c.document_version_id
			FROM prompt_contracts c JOIN pipelines p ON p.id=c.pipeline_id
			WHERE p.project_id=? AND (c.id=? OR c.document_version_id=?)
			ORDER BY c.created_at DESC, c.rowid DESC LIMIT 1`, projectID, targetID, targetID).Scan(&pipelineID, &contractID, &promptVersionID)
	}
	if pipelineID == "" {
		_ = db.QueryRowContext(ctx, `SELECT r.pipeline_id,r.document_version_id,r.source_version_id
			FROM prompt_revisions r JOIN pipelines p ON p.id=r.pipeline_id
			WHERE p.project_id=? AND r.document_version_id=?`, projectID, targetID).Scan(&pipelineID, &promptVersionID, &sbVersionID)
	}
	if pipelineID == "" {
		_ = db.QueryRowContext(ctx, `SELECT s.pipeline_id,s.document_version_id
			FROM storyboard_revisions s JOIN pipelines p ON p.id=s.pipeline_id
			WHERE p.project_id=? AND s.document_version_id=?`, projectID, targetID).Scan(&pipelineID, &sbVersionID)
	}
	if pipelineID == "" {
		var gate string
		_ = db.QueryRowContext(ctx, `SELECT a.pipeline_id,a.gate,a.document_version_id
			FROM creative_artifacts a JOIN pipelines p ON p.id=a.pipeline_id
			WHERE p.project_id=? AND a.document_version_id=?`, projectID, targetID).Scan(&pipelineID, &gate, &scriptVersionID)
		if gate == "intent" {
			intentVersionID, scriptVersionID = scriptVersionID, ""
		}
	}
	if pipelineID == "" {
		_ = db.QueryRowContext(ctx, `SELECT id FROM pipelines WHERE project_id=? AND id=?`, projectID, targetID).Scan(&pipelineID)
	}
	if pipelineID == "" {
		return out, &store.DomainError{Status: 404, Code: "not_found", Message: "Pipeline lineage not found for target"}
	}
	out.PipelineID = pipelineID

	if contractID == "" {
		q := `SELECT id,document_version_id FROM prompt_contracts WHERE pipeline_id=?`
		args := []any{pipelineID}
		if promptVersionID != "" {
			q += ` AND document_version_id=?`
			args = append(args, promptVersionID)
		}
		q += ` ORDER BY CASE status WHEN 'submitted' THEN 0 WHEN 'ready' THEN 1 ELSE 2 END, created_at DESC, rowid DESC LIMIT 1`
		_ = db.QueryRowContext(ctx, q, args...).Scan(&contractID, &promptVersionID)
	}
	if contractID != "" {
		var rawJSON, status string
		var taskID sql.NullString
		if db.QueryRowContext(ctx, `SELECT content_json,status,task_id FROM prompt_contracts WHERE id=?`, contractID).Scan(&rawJSON, &status, &taskID) == nil {
			var c PromptContract
			if json.Unmarshal([]byte(rawJSON), &c) == nil && c.ID != "" {
				c.Status = status
				c.TaskID = taskID.String
				out.Contract = &c
				if len(c.References) > 0 {
					out.References = c.References
				}
				if sbVersionID == "" {
					sbVersionID = c.StoryboardVersionID
				}
				if scriptVersionID == "" {
					scriptVersionID = c.ScriptVersionID
				}
				if intentVersionID == "" {
					intentVersionID = c.IntentVersionID
				}
			}
		}
	}

	out.Prompt = loadGateNode(ctx, db, pipelineID, "prompt", promptVersionID, 0)
	if out.Prompt != nil && sbVersionID == "" {
		sbVersionID = out.Prompt.SourceVersionID
	}
	out.Storyboard = loadGateNode(ctx, db, pipelineID, "storyboard", sbVersionID, 0)
	if out.Storyboard != nil && scriptVersionID == "" {
		_ = db.QueryRowContext(ctx, `SELECT source_version_id FROM version_dependencies WHERE version_id=? AND kind='creative_source' LIMIT 1`, out.Storyboard.DocumentVersionID).Scan(&scriptVersionID)
	}
	out.Script = loadGateNode(ctx, db, pipelineID, "script", scriptVersionID, 0)
	if out.Script != nil && intentVersionID == "" {
		_ = db.QueryRowContext(ctx, `SELECT source_version_id FROM version_dependencies WHERE version_id=? AND kind='creative_source' LIMIT 1`, out.Script.DocumentVersionID).Scan(&intentVersionID)
	}
	out.Intent = loadGateNode(ctx, db, pipelineID, "intent", intentVersionID, 0)

	versionSet := map[string]bool{}
	for _, n := range []*LineageNode{out.Intent, out.Script, out.Storyboard, out.Prompt} {
		if n != nil && n.DocumentVersionID != "" {
			versionSet[n.DocumentVersionID] = true
		}
	}

	rows, err := db.QueryContext(ctx, `SELECT o.task_id,o.attempt,o.output_key,o.role,v.resource_id,o.version_id,o.asset_id,coalesce(v.source_version_id,'')
		FROM task_outputs o
		JOIN tasks t ON t.id=o.task_id
		JOIN prompt_contracts c ON c.task_id=t.id
		JOIN versions v ON v.id=o.version_id
		WHERE c.pipeline_id=? AND o.state='delivered' AND o.version_id IS NOT NULL AND o.asset_id IS NOT NULL
		ORDER BY v.created_at, o.output_key`, pipelineID)
	if err == nil {
		for rows.Next() {
			var m DeliveredMedia
			if rows.Scan(&m.TaskID, &m.Attempt, &m.OutputKey, &m.Role, &m.ResourceID, &m.VersionID, &m.AssetID, &m.SourceVersionID) == nil {
				out.Outputs = append(out.Outputs, m)
				versionSet[m.VersionID] = true
			}
		}
		rows.Close()
	}

	depRows, err := db.QueryContext(ctx, `SELECT d.version_id,d.source_version_id,d.kind
		FROM version_dependencies d
		JOIN versions v ON v.id=d.version_id
		JOIN resources r ON r.id=v.resource_id
		JOIN compositions c ON c.id=r.composition_id
		WHERE c.project_id=?
		ORDER BY v.created_at, d.kind, d.source_version_id`, projectID)
	if err == nil {
		for depRows.Next() {
			var dep VersionDependency
			if depRows.Scan(&dep.VersionID, &dep.SourceVersionID, &dep.Kind) == nil {
				if versionSet[dep.VersionID] || versionSet[dep.SourceVersionID] {
					out.Dependencies = append(out.Dependencies, dep)
				}
			}
		}
		depRows.Close()
	}
	return out, nil
}
