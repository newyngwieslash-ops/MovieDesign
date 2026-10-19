package delivery

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/pipeline"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/workspace"
	"github.com/zeebo/xxh3"
	"io"
	"path"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

type DocumentEdit struct {
	Exact       string `json:"exact"`
	Occurrence  int    `json:"occurrence"`
	Replacement string `json:"replacement"`
}
type DocumentRequest struct {
	RequestID     string             `json:"requestId"`
	Mode          string             `json:"mode"`
	Format        string             `json:"format"`
	Content       string             `json:"content,omitempty"`
	Edits         []DocumentEdit     `json:"edits,omitempty"`
	Output        Output             `json:"output"`
	TypedDocument *pipeline.Document `json:"typedDocument,omitempty"`
}

type documentPlan struct {
	Key, Content, Origin string
	Output               Output
	TypedDocument        *pipeline.Document `json:",omitempty"`
	Noop                 *Receipt           `json:",omitempty"`
}

// ponytail: serialize document staging in this process; split by project only if
// contention warrants it. Durable identities remain in the existing write journal.
var documentsMu sync.Mutex

func (s *Service) WriteDocument(ctx context.Context, p string, raw []byte) (Receipt, error) {
	i, e := store.DecodeValue[DocumentRequest](raw)
	if e != nil {
		return Receipt{}, invalid("Invalid document schema")
	}
	if (store.Scope{ProjectID: p, Operation: "document.write", RequestID: i.RequestID}).Validate() != nil || p == "_app" {
		return Receipt{}, invalid("Invalid project_id or requestId")
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	if string(fields["typedDocument"]) == "null" {
		return Receipt{}, invalid("typedDocument cannot be null")
	}
	if i.TypedDocument != nil {
		if i.Format != "md" || i.Mode == "patch" {
			return Receipt{}, invalid("Typed storyboard requires md create/replace")
		}
		if e = i.TypedDocument.Validate(); e != nil {
			return Receipt{}, e
		}
		if len(fields["content"]) != 0 && i.TypedDocument.Kind == "storyboard" {
			candidate, err := pipeline.ParseMarkdown(i.Content)
			if err != nil {
				return Receipt{}, err
			}
			a, _ := json.Marshal(candidate)
			b, _ := json.Marshal(i.TypedDocument.Storyboard)
			if string(a) != string(b) {
				return Receipt{}, invalid("Markdown candidate and typed storyboard disagree")
			}
		}
		if i.TypedDocument.Kind != "storyboard" && len(fields["content"]) != 0 && i.Content != i.TypedDocument.Render() {
			return Receipt{}, invalid("Markdown and creative artifact disagree")
		}
		i.Content = i.TypedDocument.Render()
	}
	if i.TypedDocument == nil && i.Mode != "patch" && (len(fields["content"]) == 0 || string(fields["content"]) == "null") {
		return Receipt{}, invalid("Create/replace requires explicit complete content")
	}
	if e = ValidateOutput(i.Output); e != nil {
		return Receipt{}, e
	}
	if len(i.Output.Resources) != 1 || (i.Mode != "create" && i.Mode != "replace" && i.Mode != "patch") || (i.Mode == "create") != (i.Output.Resources[0].Mode == "create") {
		return Receipt{}, invalid("Declare exactly one document; revisions require new_version and baseVersionId")
	}
	switch i.Format {
	case "md", "txt", "srt", "ass":
	default:
		return Receipt{}, invalid("Unsupported document format")
	}
	if i.Mode == "patch" && (i.Content != "" || len(i.Edits) == 0 || len(i.Edits) > 100) || i.Mode != "patch" && len(i.Edits) != 0 {
		return Receipt{}, invalid("Patch requires edits only; create/replace requires complete content")
	}
	documentsMu.Lock()
	defer documentsMu.Unlock()
	missingPlan := errors.New("document plan not frozen")
	scope := store.Scope{ProjectID: p, Operation: "document.write", RequestID: i.RequestID}
	encoded, e := store.WriteRequest(ctx, s.db, scope, raw, func(*sql.Tx) ([]byte, error) { return nil, missingPlan })
	if e != nil && !errors.Is(e, missingPlan) {
		return Receipt{}, e
	}
	prepare := func() ([]byte, error) {
		content := i.Content
		if i.Mode == "patch" {
			var v string
			e := s.db.QueryRowContext(ctx, `SELECT a.path FROM versions v JOIN resources r ON r.id=v.resource_id JOIN compositions c ON c.id=r.composition_id JOIN assets a ON a.id=v.asset_id WHERE v.id=? AND c.project_id=? AND a.status='active'`, i.Output.Resources[0].BaseVersionID, p).Scan(&v)
			if e != nil {
				return nil, invalid("Base document unavailable in this project")
			}
			if strings.TrimPrefix(strings.ToLower(path.Ext(v)), ".") != i.Format {
				return nil, invalid("Patch format must match the base document")
			}
			f, e := s.workspace.OpenRegular(p, v)
			if e != nil {
				return nil, e
			}
			b, e := io.ReadAll(io.LimitReader(f, workspace.DocumentLimit+1))
			f.Close()
			if e != nil {
				return nil, e
			}
			content = string(b)
			if len(content) > workspace.DocumentLimit || !utf8.ValidString(content) || strings.ContainsRune(content, 0) {
				return nil, invalid("Base document must be UTF-8 text <= 1 MiB")
			}
			type edit struct {
				start, end  int
				replacement string
			}
			edits := []edit{}
			for _, v := range i.Edits {
				if v.Exact == "" || v.Occurrence < 1 {
					return nil, invalid("Patch anchors require exact text and positive occurrence")
				}
				at, from := -1, 0
				for n := 0; n < v.Occurrence; n++ {
					next := strings.Index(content[from:], v.Exact)
					if next < 0 {
						return nil, invalid("Patch anchor not found")
					}
					at = from + next
					from = at + len(v.Exact)
				}
				edits = append(edits, edit{at, at + len(v.Exact), v.Replacement})
			}
			sort.Slice(edits, func(a, b int) bool { return edits[a].start < edits[b].start })
			for n := 1; n < len(edits); n++ {
				if edits[n].start < edits[n-1].end {
					return nil, invalid("Overlapping patch anchors")
				}
			}
			for n := len(edits) - 1; n >= 0; n-- {
				v := edits[n]
				content = content[:v.start] + v.replacement + content[v.end:]
			}
		}
		if len(content) > workspace.DocumentLimit || !utf8.ValidString(content) || strings.ContainsRune(content, 0) {
			return nil, invalid("Document must be UTF-8 text <= 1 MiB")
		}
		return json.Marshal(documentPlan{Key: uuid.NewString(), Content: content, Origin: s.origin, Output: i.Output, TypedDocument: i.TypedDocument})
	}
	if errors.Is(e, missingPlan) {
		prepared, err := prepare()
		if err != nil {
			return Receipt{}, err
		}
		encoded, e = store.WriteRequest(ctx, s.db, scope, raw, func(tx *sql.Tx) ([]byte, error) {
			if i.TypedDocument != nil {
				version, err := pipeline.Check(ctx, tx, p, *i.TypedDocument, i.Output.Resources[0].BaseVersionID)
				if err != nil {
					return nil, err
				}
				if version != "" {
					c := i.Output.Composition
					target, err := store.ResolveDeliveryTarget(ctx, tx, p, c.Type.ID, c.Type.Label, c.Name)
					if err != nil {
						return nil, err
					}
					var composition string
					if err = tx.QueryRowContext(ctx, "SELECT r.composition_id FROM versions v JOIN resources r ON r.id=v.resource_id WHERE v.id=?", version).Scan(&composition); err != nil {
						return nil, err
					}
					if target.NewComposition || target.CompositionID != composition {
						return nil, invalid("No-op output must target the existing storyboard composition")
					}
					var roles int
					if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM composition_members m JOIN versions v ON v.resource_id=m.resource_id WHERE v.id=? AND m.composition_id=? AND m.role=?`, version, composition, c.Placements[0].Role).Scan(&roles); err != nil {
						return nil, err
					}
					if roles == 0 {
						return nil, invalid("Semantic no-op cannot add a new placement role")
					}
					var receipt string
					err = tx.QueryRowContext(ctx, `SELECT d.receipt FROM deliveries d JOIN delivery_sources s ON s.project_id=d.project_id AND s.request_id=d.request_id WHERE d.project_id=? AND d.status='completed' AND s.version_id=?`, p, version).Scan(&receipt)
					if err != nil {
						return nil, err
					}
					var plan documentPlan
					_ = json.Unmarshal(prepared, &plan)
					plan.Noop = &Receipt{}
					if err = json.Unmarshal([]byte(receipt), plan.Noop); err != nil {
						return nil, err
					}
					plan.Noop.Delivery.Versions[0].Key = i.Output.Resources[0].Key
					return json.Marshal(plan)
				}
			}
			if i.TypedDocument != nil {
				source, err := pipeline.ApprovedSource(ctx, tx, p, i.TypedDocument.PipelineID, i.TypedDocument.Kind)
				if err != nil {
					return nil, err
				}
				var plan documentPlan
				if err = json.Unmarshal(prepared, &plan); err != nil {
					return nil, err
				}
				plan.TypedDocument.SourceVersionID = source
				return json.Marshal(plan)
			}
			return prepared, nil
		})
		if e != nil {
			return Receipt{}, e
		}
	}
	var planned documentPlan
	if e = json.Unmarshal(encoded, &planned); e != nil {
		return Receipt{}, e
	}
	if planned.Noop != nil {
		return *planned.Noop, nil
	}
	requestID := "document-" + planned.Key
	if j, e := s.load(ctx, p, requestID); e != nil && !errors.Is(e, sql.ErrNoRows) {
		return Receipt{}, e
	} else if j != nil {
		return s.Recover(ctx, p, requestID)
	}
	files := workspace.NewService(s.db, s.workspace)
	name := "document." + i.Format
	if planned.TypedDocument != nil {
		name = planned.TypedDocument.Kind + ".md"
		if planned.TypedDocument.Kind == "storyboard" {
			name = "storyboard_review.md"
		}
		if planned.TypedDocument.Kind == "prompt" {
			name = "final_video_prompt.md"
		}
	}
	stage, e := files.Stage(ctx, p, requestID, name, strings.NewReader(planned.Content))
	if e != nil {
		return Receipt{}, e
	}
	owned := *s
	owned.origin = planned.Origin
	return owned.Deliver(ctx, p, Request{RequestID: requestID, Output: planned.Output, Sources: []Source{{Key: planned.Output.Resources[0].Key, Path: stage}}})
}

// Recovery uses the original frozen document plan, including trusted actor and
// typed semantics, even when entered through generic Delivery.Recover.
func commitDocument(ctx context.Context, tx *sql.Tx, p, key string, j *journal, versions []Version) error {
	if !strings.HasPrefix(key, "document-") {
		return nil
	}
	var raw string
	e := tx.QueryRowContext(ctx, `SELECT response_json FROM write_requests WHERE project_id=? AND operation='document.write' AND json_extract(response_json,'$.Key')=?`, p, strings.TrimPrefix(key, "document-")).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	var plan documentPlan
	if e = json.Unmarshal([]byte(raw), &plan); e != nil {
		return e
	}
	if plan.TypedDocument == nil {
		return nil
	}
	var actual Request
	if e = json.Unmarshal([]byte(j.Input), &actual); e != nil {
		return e
	}
	want, _ := json.Marshal(plan.Output)
	got, _ := json.Marshal(actual.Output)
	if j.Origin != plan.Origin || string(want) != string(got) || len(j.Plan.Sources) != 1 || j.Plan.Sources[0].Asset.FullHash == nil || *j.Plan.Sources[0].Asset.FullHash != fmt.Sprintf("%016x", xxh3.HashString(plan.Content)) {
		return invalid("Document artifact differs from its frozen plan")
	}
	d := plan.TypedDocument
	v := versions[0]
	if _, e = pipeline.Check(ctx, tx, p, *d, plan.Output.Resources[0].BaseVersionID); e != nil {
		return e
	}
	if d.Kind == "prompt" {
		return pipeline.SavePrompt(ctx, tx, p, *d, plan.Output.Resources[0].BaseVersionID, v.ResourceID, v.VersionID)
	}
	if d.Kind != "storyboard" {
		return pipeline.SaveArtifact(ctx, tx, p, *d, plan.Output.Resources[0].BaseVersionID, v.ResourceID, v.VersionID)
	}
	_, e = pipeline.SaveStoryboard(ctx, tx, store.Scope{ProjectID: p, Operation: "document.write", RequestID: key}, key, d.PipelineID, d.ExpectedRevision, d.Storyboard, plan.Output.Resources[0].BaseVersionID, v.ResourceID, v.VersionID)
	return e
}
