package delivery

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/workspace"
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
	RequestID string         `json:"requestId"`
	Mode      string         `json:"mode"`
	Format    string         `json:"format"`
	Content   string         `json:"content,omitempty"`
	Edits     []DocumentEdit `json:"edits,omitempty"`
	Output    Output         `json:"output"`
}

// ponytail: serialize document staging in this process; split by project only if
// contention warrants it. Durable identities remain in the existing write journal.
var documentsMu sync.Mutex

func (s *Service) WriteDocument(ctx context.Context, p string, raw []byte) (Receipt, error) {
	i, e := store.DecodeValue[DocumentRequest](raw)
	if e != nil {
		return Receipt{}, invalid("Invalid document schema")
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	if i.Mode != "patch" && (len(fields["content"]) == 0 || string(fields["content"]) == "null") {
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
	type plan struct {
		Key, Content, Origin string
		Output               Output
	}
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
		return json.Marshal(plan{uuid.NewString(), content, s.origin, i.Output})
	}
	if errors.Is(e, missingPlan) {
		prepared, err := prepare()
		if err != nil {
			return Receipt{}, err
		}
		encoded, e = store.WriteRequest(ctx, s.db, scope, raw, func(*sql.Tx) ([]byte, error) { return prepared, nil })
		if e != nil {
			return Receipt{}, e
		}
	}
	var planned plan
	if e = json.Unmarshal(encoded, &planned); e != nil {
		return Receipt{}, e
	}
	requestID := "document-" + planned.Key
	if j, e := s.load(ctx, p, requestID); e != nil && !errors.Is(e, sql.ErrNoRows) {
		return Receipt{}, e
	} else if j != nil {
		return s.Recover(ctx, p, requestID)
	}
	files := workspace.NewService(s.db, s.workspace)
	stage, e := files.Stage(ctx, p, requestID, "document."+i.Format, strings.NewReader(planned.Content))
	if e != nil {
		return Receipt{}, e
	}
	owned := *s
	owned.origin = planned.Origin
	return owned.Deliver(ctx, p, Request{RequestID: requestID, Output: planned.Output, Sources: []Source{{Key: planned.Output.Resources[0].Key, Path: stage}}})
}
