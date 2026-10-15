package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"sort"
	"strings"
	"unicode/utf16"
)

type Canvas struct{ DB *sql.DB }
type CanvasDocument struct {
	SchemaVersion int                       `json:"schemaVersion"`
	Revision      int64                     `json:"revision"`
	Nodes         map[string]map[string]any `json:"nodes"`
	Edges         map[string]map[string]any `json:"edges"`
}
type CanvasEffects struct {
	Undo           any      `json:"undo"`
	CompositionIDs []string `json:"compositionIds"`
}
type CanvasReceipt struct {
	RequestID       string         `json:"requestId"`
	AppliedRevision int64          `json:"appliedRevision"`
	Document        CanvasDocument `json:"document"`
	Replayed        bool           `json:"replayed"`
	Effects         CanvasEffects  `json:"effects"`
}
type CanvasError struct {
	Status        int
	Kind, Message string
}

func (e *CanvasError) Error() string                     { return e.Message }
func canvasError(status int, kind, message string) error { return &CanvasError{status, kind, message} }
func canvasInvalid(message string) error {
	return canvasError(400, "CANVAS_V3_INVALID_COMMAND", message)
}
func emptyCanvas() CanvasDocument {
	return CanvasDocument{1, 0, map[string]map[string]any{}, map[string]map[string]any{}}
}
func readCanvas(ctx context.Context, tx *sql.Tx, p string) (CanvasDocument, error) {
	d := emptyCanvas()
	var raw string
	e := tx.QueryRowContext(ctx, "SELECT document_json FROM canvas_documents WHERE project_id=?", p).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return d, nil
	}
	if e != nil {
		return d, e
	}
	e = json.Unmarshal([]byte(raw), &d)
	return d, e
}
func (c Canvas) Document(ctx context.Context, p string) (CanvasDocument, error) {
	tx, e := c.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return emptyCanvas(), domainError(e)
	}
	defer tx.Rollback()
	if e = requireProject(ctx, tx, p); e != nil {
		return emptyCanvas(), domainError(e)
	}
	d, e := readCanvas(ctx, tx, p)
	return d, domainError(e)
}
func canvasID(v any) bool {
	s, ok := v.(string)
	return ok && strings.TrimSpace(s) != "" && len(utf16.Encode([]rune(s))) <= 256 && s != "__proto__" && s != "prototype" && s != "constructor"
}
func canvasText(v any, max int) bool {
	s, ok := v.(string)
	return ok && len(utf16.Encode([]rune(s))) <= max
}
func number(v any) bool { n, ok := v.(float64); return ok && !math.IsInf(n, 0) && !math.IsNaN(n) }
func integer(v any) bool {
	return number(v) && math.Trunc(v.(float64)) == v.(float64) && math.Abs(v.(float64)) <= 9007199254740991
}
func fields(v any, required string, optional string) (map[string]any, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, canvasInvalid("Expected object")
	}
	allowed := map[string]bool{}
	for _, k := range strings.Fields(required) {
		allowed[k] = true
		if _, ok := m[k]; !ok {
			return nil, canvasInvalid("Missing field " + k)
		}
	}
	for _, k := range strings.Fields(optional) {
		allowed[k] = true
	}
	for k := range m {
		if !allowed[k] {
			return nil, canvasInvalid("Unknown field " + k)
		}
	}
	return m, nil
}
func generic(v any, depth, limit int) error {
	if depth > 32 {
		return canvasInvalid("JSON nesting exceeds 32")
	}
	switch x := v.(type) {
	case map[string]any:
		for k, v := range x {
			if k == "__proto__" || k == "prototype" || k == "constructor" {
				return canvasInvalid("Unsafe JSON key")
			}
			if e := generic(v, depth+1, limit); e != nil {
				return e
			}
		}
	case []any:
		if len(x) > limit {
			return canvasInvalid("JSON array exceeds limit")
		}
		for _, v := range x {
			if e := generic(v, depth+1, limit); e != nil {
				return e
			}
		}
	case float64:
		if !number(x) {
			return canvasInvalid("Nonfinite number")
		}
	}
	return nil
}
func point(v any, sized bool) error {
	a, b := "x", "y"
	if sized {
		a, b = "width", "height"
	}
	m, e := fields(v, a+" "+b, "")
	if e != nil {
		return e
	}
	if !number(m[a]) || !number(m[b]) || sized && (m[a].(float64) <= 0 || m[b].(float64) <= 0) {
		return canvasInvalid("Invalid geometry")
	}
	return nil
}
func selection(v any, history bool) error {
	m, ok := v.(map[string]any)
	if !ok {
		return canvasInvalid("Invalid selection")
	}
	keys := ""
	switch m["type"] {
	case "version":
		keys = "resourceId versionId"
	case "task-output":
		keys = "taskId outputKey"
	case "generation-request":
		if history {
			keys = "requestId outputKey"
		}
	}
	if keys == "" {
		return canvasInvalid("Invalid item ref")
	}
	m, e := fields(v, "type "+keys, "")
	if e != nil {
		return e
	}
	for _, k := range strings.Fields(keys) {
		if !canvasID(m[k]) {
			return canvasInvalid("Invalid selection identifier")
		}
	}
	return nil
}
func draft(v any) error {
	if v == nil {
		return nil
	}
	m, e := fields(v, "modelId prompt params references outputCount", "destination excludedReferenceVersionIds lastFrameOnly")
	if e != nil {
		return e
	}
	if m["modelId"] != nil && !canvasID(m["modelId"]) || !canvasText(m["prompt"], 20000) || !integer(m["outputCount"]) || m["outputCount"].(float64) < 1 || m["outputCount"].(float64) > 8 {
		return canvasInvalid("Invalid generation draft")
	}
	if _, ok := m["params"].(map[string]any); !ok {
		return canvasInvalid("Invalid params")
	}
	if e := generic(m["params"], 0, 10000); e != nil {
		return e
	}
	refs, ok := m["references"].([]any)
	if !ok || len(refs) > 64 {
		return canvasInvalid("Invalid references")
	}
	for _, v := range refs {
		r, e := fields(v, "resourceId versionId", "")
		if e != nil {
			return e
		}
		if !canvasID(r["resourceId"]) || !canvasID(r["versionId"]) {
			return canvasInvalid("Invalid reference")
		}
	}
	if v, ok := m["destination"]; ok && v != "new" && v != "in_place" {
		return canvasInvalid("Invalid destination")
	}
	if v, ok := m["lastFrameOnly"]; ok && v != true {
		return canvasInvalid("Invalid lastFrameOnly")
	}
	if v, ok := m["excludedReferenceVersionIds"]; ok {
		a, ok := v.([]any)
		if !ok || len(a) > 64 {
			return canvasInvalid("Invalid exclusions")
		}
		for _, v := range a {
			if !canvasID(v) {
				return canvasInvalid("Invalid exclusion")
			}
		}
	}
	return nil
}
func canvasNode(v any) (map[string]any, error) {
	raw, ok := v.(map[string]any)
	if !ok {
		return nil, canvasInvalid("Invalid node")
	}
	required := "id parentId position size zIndex hidden kind"
	optional := ""
	switch raw["kind"] {
	case "frame":
		required += " title collapsed"
		optional = "backgroundColor"
	case "composition":
		required += " compositionId activeItem draft"
		optional = "containerKind subjectLayout hiddenLineage hiddenTaskOutputs pendingGeneration memberSelections presentation"
	case "placeholder":
		required += " expectedKind draft"
		optional = "presentation hiddenLineage pendingGeneration"
	default:
		return nil, canvasInvalid("Unsupported node kind")
	}
	m, e := fields(v, required, optional)
	if e != nil {
		return nil, e
	}
	if !canvasID(m["id"]) || m["parentId"] != nil && !canvasID(m["parentId"]) || !integer(m["zIndex"]) {
		return nil, canvasInvalid("Invalid node base")
	}
	if _, ok := m["hidden"].(bool); !ok {
		return nil, canvasInvalid("Invalid hidden")
	}
	if e = point(m["position"], false); e != nil {
		return nil, e
	}
	if e = point(m["size"], true); e != nil {
		return nil, e
	}
	if m["kind"] == "frame" {
		if !canvasText(m["title"], 4096) {
			return nil, canvasInvalid("Invalid title")
		}
		if _, ok := m["collapsed"].(bool); !ok {
			return nil, canvasInvalid("Invalid collapsed")
		}
		if v, ok := m["backgroundColor"]; ok && !contains("red orange yellow green cyan blue purple", v) {
			return nil, canvasInvalid("Invalid frame color")
		}
		return m, nil
	}
	if e = draft(m["draft"]); e != nil {
		return nil, e
	}
	if m["kind"] == "composition" {
		if !canvasID(m["compositionId"]) {
			return nil, canvasInvalid("Invalid composition")
		}
		if m["activeItem"] != nil {
			if e = selection(m["activeItem"], false); e != nil {
				return nil, e
			}
		}
		if v, ok := m["containerKind"]; ok && v != "subject" {
			return nil, canvasInvalid("Invalid container")
		}
		if v, ok := m["memberSelections"]; ok {
			s, ok := v.(map[string]any)
			if !ok || len(s) > 10000 {
				return nil, canvasInvalid("Invalid member selections")
			}
			for k, v := range s {
				if !canvasID(k) {
					return nil, canvasInvalid("Invalid member id")
				}
				if e = selection(v, true); e != nil {
					return nil, e
				}
			}
		}
		if v, ok := m["subjectLayout"]; ok {
			if e = subjectLayout(v); e != nil {
				return nil, e
			}
		}
	} else if !contains("image video audio text file table subject", m["expectedKind"]) {
		return nil, canvasInvalid("Invalid expectedKind")
	}
	if v, ok := m["presentation"]; ok {
		if v == "subject" {
			if m["kind"] == "composition" {
				m["containerKind"] = "subject"
			} else {
				m["expectedKind"] = "subject"
			}
			delete(m, "presentation")
		} else if v != "panorama" || m["kind"] == "placeholder" && m["expectedKind"] != "image" {
			return nil, canvasInvalid("Invalid presentation")
		}
	}
	if v, ok := m["pendingGeneration"]; ok {
		g, e := fields(v, "requestId outputKey", "")
		if e != nil {
			return nil, e
		}
		if !canvasID(g["requestId"]) || !canvasID(g["outputKey"]) {
			return nil, canvasInvalid("Invalid pending generation")
		}
	}
	for _, k := range []string{"hiddenLineage", "hiddenTaskOutputs"} {
		if v, ok := m[k]; ok {
			a, ok := v.([]any)
			if !ok || len(a) > 20000 {
				return nil, canvasInvalid("Invalid hidden references")
			}
			for _, v := range a {
				req, opt := "taskId outputKey", ""
				if k == "hiddenLineage" {
					req, opt = "taskId sourceNodeId referenceVersionId", "outputKey"
				}
				h, e := fields(v, req, opt)
				if e != nil {
					return nil, e
				}
				for _, v := range h {
					if !canvasID(v) {
						return nil, canvasInvalid("Invalid hidden reference")
					}
				}
			}
		}
	}
	return m, nil
}
func contains(values string, v any) bool {
	s, ok := v.(string)
	return ok && slices.Contains(strings.Fields(values), s)
}
func subjectLayout(v any) error {
	if e := generic(v, 0, 10000); e != nil {
		return e
	}
	m, e := fields(v, "preset columns sections spans rows", "arrangement version panoramaResources")
	if e != nil {
		return e
	}
	if !contains("gallery board", m["preset"]) || !integer(m["columns"]) || m["columns"].(float64) < 1 || m["columns"].(float64) > 3 {
		return canvasInvalid("Invalid subject layout")
	}
	if a, ok := m["arrangement"]; ok && a != "masonry" {
		return canvasInvalid("Invalid arrangement")
	}
	delete(m, "version")
	sections, ok := m["sections"].([]any)
	if !ok || len(sections) > 100 {
		return canvasInvalid("Invalid sections")
	}
	ids, owners := map[string]bool{}, map[string]string{}
	for _, v := range sections {
		s, e := fields(v, "id title resources", "")
		if e != nil {
			return e
		}
		if !canvasID(s["id"]) || !canvasText(s["title"], 200) {
			return canvasInvalid("Invalid section")
		}
		id := s["id"].(string)
		if ids[id] {
			return canvasInvalid("Duplicate section")
		}
		ids[id] = true
		a, ok := s["resources"].([]any)
		if !ok {
			return canvasInvalid("Invalid section resources")
		}
		for _, r := range a {
			if !canvasID(r) {
				return canvasInvalid("Invalid resource")
			}
			r := r.(string)
			if owners[r] != "" {
				return canvasInvalid("Duplicate resource")
			}
			owners[r] = id
		}
	}
	if !ids["default"] {
		return canvasInvalid("Missing default section")
	}
	spans, ok := m["spans"].([]any)
	if !ok {
		return canvasInvalid("Invalid spans")
	}
	seen := map[string]bool{}
	for _, v := range spans {
		s, e := fields(v, "resourceId span", "")
		if e != nil {
			return e
		}
		if !canvasID(s["resourceId"]) || s["span"] != "full" && s["span"] != float64(1) && s["span"] != float64(2) {
			return canvasInvalid("Invalid span")
		}
		id := s["resourceId"].(string)
		if seen[id] {
			return canvasInvalid("Duplicate span")
		}
		seen[id] = true
	}
	rows, ok := m["rows"].([]any)
	if !ok {
		return canvasInvalid("Invalid rows")
	}
	seen = map[string]bool{}
	for _, v := range rows {
		r, e := fields(v, "sectionId region resources", "")
		if e != nil {
			return e
		}
		if !canvasID(r["sectionId"]) || !ids[r["sectionId"].(string)] || !contains("visual details", r["region"]) {
			return canvasInvalid("Invalid row")
		}
		a, ok := r["resources"].([]any)
		if !ok {
			return canvasInvalid("Invalid row resources")
		}
		for _, v := range a {
			if !canvasID(v) {
				return canvasInvalid("Invalid resource")
			}
			id := v.(string)
			if seen[id] || owners[id] != "" && owners[id] != r["sectionId"] {
				return canvasInvalid("Invalid row ownership")
			}
			seen[id] = true
		}
	}
	if v, ok := m["panoramaResources"]; ok {
		a, ok := v.([]any)
		if !ok {
			return canvasInvalid("Invalid panorama resources")
		}
		out := []any{}
		seen = map[string]bool{}
		for _, v := range a {
			if !canvasID(v) {
				return canvasInvalid("Invalid panorama resource")
			}
			id := v.(string)
			if !seen[id] {
				out = append(out, id)
				seen[id] = true
			}
		}
		m["panoramaResources"] = out
	}
	return nil
}
func canvasEdge(v any) (map[string]any, error) {
	m, e := fields(v, "id source target", "sourceResourceId")
	if e != nil {
		return nil, e
	}
	for _, v := range m {
		if !canvasID(v) {
			return nil, canvasInvalid("Invalid edge identifier")
		}
	}
	if m["source"] == m["target"] {
		return nil, canvasInvalid("Self edge")
	}
	return m, nil
}
func validateGraph(d CanvasDocument) error {
	if len(d.Nodes) > 10000 || len(d.Edges) > 20000 {
		return canvasInvalid("Canvas graph exceeds limit")
	}
	for id, n := range d.Nodes {
		if n["id"] != id {
			return canvasInvalid("Node id mismatch")
		}
		if p := n["parentId"]; p != nil {
			parent := d.Nodes[p.(string)]
			if parent == nil || parent["kind"] != "frame" {
				return canvasInvalid("Parent must be an existing frame")
			}
		}
	}
	done := map[string]bool{}
	for id := range d.Nodes {
		path := map[string]bool{}
		for id != "" && !done[id] {
			if path[id] {
				return canvasInvalid("Parent cycle")
			}
			path[id] = true
			p := d.Nodes[id]["parentId"]
			id = ""
			if p != nil {
				id = p.(string)
			}
		}
		for id := range path {
			done[id] = true
		}
	}
	for id, e := range d.Edges {
		if e["id"] != id || d.Nodes[id] != nil {
			return canvasInvalid("Duplicate element id")
		}
		if d.Nodes[e["source"].(string)] == nil || d.Nodes[e["target"].(string)] == nil {
			return canvasInvalid("Missing edge endpoint")
		}
	}
	return nil
}
func applyCommand(d *CanvasDocument, v any, nested bool) (any, error) {
	raw, ok := v.(map[string]any)
	if !ok {
		return nil, canvasInvalid("Invalid command")
	}
	if raw["type"] == "canvas.batch" {
		m, e := fields(v, "type commands", "")
		if e != nil {
			return nil, e
		}
		a, ok := m["commands"].([]any)
		if nested || !ok || len(a) == 0 || len(a) > 64 {
			return nil, canvasInvalid("Invalid batch")
		}
		undos := []any{}
		for _, cmd := range a {
			u, e := applyCommand(d, cmd, true)
			if e != nil {
				return nil, e
			}
			undos = append([]any{u}, undos...)
		}
		if len(undos) == 1 {
			return undos[0], nil
		}
		return map[string]any{"type": "canvas.batch", "commands": undos}, nil
	}
	if raw["type"] != "canvas.patch" {
		return nil, canvasInvalid("Unsupported non-MVP content command")
	}
	m, e := fields(v, "type nodes edges", "")
	if e != nil {
		return nil, e
	}
	seen := map[string]bool{}
	undo := map[string]any{"type": "canvas.patch"}
	count := 0
	for _, kind := range []string{"nodes", "edges"} {
		changes, ok := m[kind].([]any)
		if !ok {
			return nil, canvasInvalid("Changes must be arrays")
		}
		count += len(changes)
		if count > 30000 {
			return nil, canvasInvalid("Too many changes")
		}
		items := d.Nodes
		parse := canvasNode
		if kind == "edges" {
			items = d.Edges
			parse = canvasEdge
		}
		reverse := []any{}
		for _, v := range changes {
			ch, e := fields(v, "id before after", "")
			if e != nil {
				return nil, e
			}
			if !canvasID(ch["id"]) {
				return nil, canvasInvalid("Invalid change id")
			}
			id := ch["id"].(string)
			if seen[id] {
				return nil, canvasInvalid("Duplicate change")
			}
			seen[id] = true
			for _, k := range []string{"before", "after"} {
				if ch[k] != nil {
					obj, e := parse(ch[k])
					if e != nil {
						return nil, e
					}
					if obj["id"] != id {
						return nil, canvasInvalid("Change id mismatch")
					}
					ch[k] = obj
				}
			}
			if reflect.DeepEqual(ch["before"], ch["after"]) {
				return nil, canvasInvalid("Noop change")
			}
			var current any
			if items[id] != nil {
				current = items[id]
			}
			if !reflect.DeepEqual(current, ch["before"]) {
				return nil, canvasError(409, "CANVAS_V3_BEFORE_MISMATCH", "Patch before does not match current node")
			}
			if kind == "nodes" && current != nil && ch["after"] != nil {
				a, b := current.(map[string]any), ch["after"].(map[string]any)
				if a["kind"] == "composition" && b["kind"] == "composition" && a["compositionId"] != b["compositionId"] {
					return nil, canvasError(409, "CANVAS_V3_CONTENT_IDENTITY", "Composition identity cannot be replaced")
				}
			}
			if ch["after"] == nil {
				delete(items, id)
			} else {
				items[id] = ch["after"].(map[string]any)
			}
			reverse = append(reverse, map[string]any{"id": id, "before": ch["after"], "after": ch["before"]})
		}
		undo[kind] = reverse
	}
	if count == 0 {
		return nil, canvasInvalid("Empty patch")
	}
	if e = validateGraph(*d); e != nil {
		return nil, e
	}
	return undo, nil
}

// References cover current state and durable undo effects. The one SQLite writer
// serializes validation, graph publication and receipt creation with object writes.
func canvasRefs(v any, refs map[string]map[string]bool) {
	switch x := v.(type) {
	case map[string]any:
		for k, v := range x {
			if k == "params" {
				continue
			} // Draft parameters are JSON, not object references.
			kind := ""
			switch k {
			case "compositionId":
				kind = "compositions"
			case "resourceId", "sourceResourceId":
				kind = "resources"
			case "versionId", "referenceVersionId":
				kind = "versions"
			case "taskId":
				kind = "tasks"
			}
			if s, ok := v.(string); ok && kind != "" {
				refs[kind][s] = true
			}
			if k == "memberSelections" {
				if m, ok := v.(map[string]any); ok {
					for id := range m {
						refs["resources"][id] = true
					}
				}
			}
			if k == "excludedReferenceVersionIds" || k == "panoramaResources" || k == "resources" {
				if a, ok := v.([]any); ok {
					for _, v := range a {
						if s, ok := v.(string); ok {
							kind := "resources"
							if k == "excludedReferenceVersionIds" {
								kind = "versions"
							}
							refs[kind][s] = true
						}
					}
				}
			}
			canvasRefs(v, refs)
		}
	case []any:
		for _, v := range x {
			canvasRefs(v, refs)
		}
	}
}
func references(v any) map[string]map[string]bool {
	refs := map[string]map[string]bool{}
	for _, k := range []string{"compositions", "resources", "versions", "tasks"} {
		refs[k] = map[string]bool{}
	}
	canvasRefs(v, refs)
	return refs
}
func checkCanvasReferences(ctx context.Context, tx *sql.Tx, p string, d CanvasDocument, undo any) (map[string]map[string]bool, error) {
	raw, _ := json.Marshal(d)
	var v any
	json.Unmarshal(raw, &v)
	if e := checkCanvasPairs(ctx, tx, v); e != nil {
		return nil, e
	}
	refs := references(v)
	canvasRefs(undo, refs)
	for kind, ids := range refs {
		for id := range ids {
			if kind == "tasks" {
				return nil, canvasError(400, "CANVAS_V3_ITEM_UNAVAILABLE", "Task does not exist; task storage arrives in M4")
			}
			if _, e := getObject(ctx, tx, p, kind, id); e != nil {
				return nil, canvasError(400, "CANVAS_V3_ITEM_UNAVAILABLE", "Reference not found in this project")
			}
		}
	}
	for _, n := range d.Nodes {
		if n["kind"] != "composition" {
			continue
		}
		cid := n["compositionId"].(string)
		check := func(v any, member string) error {
			if v == nil {
				return nil
			}
			r := v.(map[string]any)
			if r["type"] == "generation-request" {
				return nil
			}
			if r["type"] != "version" {
				return canvasInvalid("Task does not exist")
			}
			rid, vid := r["resourceId"].(string), r["versionId"].(string)
			var count int
			e := tx.QueryRowContext(ctx, `SELECT count(*) FROM versions v WHERE v.id=? AND v.resource_id=? AND (EXISTS(SELECT 1 FROM resources r WHERE r.id=v.resource_id AND r.composition_id=?) OR EXISTS(SELECT 1 FROM composition_members m WHERE m.resource_id=v.resource_id AND m.composition_id=?))`, vid, rid, cid, cid).Scan(&count)
			if e != nil {
				return e
			}
			if count != 1 || member != "" && member != rid {
				return canvasInvalid("Selected version does not belong to composition/member")
			}
			return nil
		}
		if e := check(n["activeItem"], ""); e != nil {
			return nil, e
		}
		if m, ok := n["memberSelections"].(map[string]any); ok {
			for id, v := range m {
				var count int
				if e := tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM composition_members WHERE composition_id=? AND resource_id=?)+(SELECT count(*) FROM resources WHERE composition_id=? AND id=?)`, cid, id, cid, id).Scan(&count); e != nil {
					return nil, e
				}
				if count == 0 {
					return nil, canvasInvalid("Selected resource is not a composition member")
				}
				if e := check(v, id); e != nil {
					return nil, e
				}
			}
		}
	}
	return refs, nil
}

func checkCanvasPairs(ctx context.Context, tx *sql.Tx, v any) error {
	switch x := v.(type) {
	case map[string]any:
		if rid, ok := x["resourceId"].(string); ok {
			if vid, ok := x["versionId"].(string); ok {
				var n int
				if e := tx.QueryRowContext(ctx, "SELECT count(*) FROM versions WHERE id=? AND resource_id=?", vid, rid).Scan(&n); e != nil {
					return e
				}
				if n != 1 {
					return canvasInvalid("Version does not belong to referenced resource")
				}
			}
		}
		for k, v := range x {
			if k != "params" {
				if e := checkCanvasPairs(ctx, tx, v); e != nil {
					return e
				}
			}
		}
	case []any:
		for _, v := range x {
			if e := checkCanvasPairs(ctx, tx, v); e != nil {
				return e
			}
		}
	}
	return nil
}
func (c Canvas) Execute(ctx context.Context, p string, input []byte) (out CanvasReceipt, err error) {
	canonical, e := CanonicalJSON(input)
	if e != nil {
		return out, canvasInvalid(e.Error())
	}
	var raw any
	if e = json.Unmarshal(canonical, &raw); e != nil {
		return out, canvasInvalid(e.Error())
	}
	if e = generic(raw, 0, 30000); e != nil {
		return out, e
	}
	m, e := fields(raw, "requestId expectedRevision command", "")
	if e != nil {
		return out, e
	}
	if !canvasID(m["requestId"]) || !integer(m["expectedRevision"]) || m["expectedRevision"].(float64) < 0 {
		return out, canvasInvalid("Invalid request")
	}
	key := m["requestId"].(string)
	tx, e := c.DB.BeginTx(ctx, nil)
	if e != nil {
		return out, domainError(e)
	}
	defer tx.Rollback()
	if e = requireProject(ctx, tx, p); e != nil {
		return out, domainError(e)
	}
	d, e := readCanvas(ctx, tx, p)
	if e != nil {
		return out, domainError(e)
	}
	out.RequestID = key
	out.Document = d
	var previous, effects string
	e = tx.QueryRowContext(ctx, "SELECT input_json,applied_revision,effects_json FROM canvas_commands WHERE project_id=? AND request_id=?", p, key).Scan(&previous, &out.AppliedRevision, &effects)
	if e == nil {
		if previous != string(canonical) {
			return out, canvasError(409, "CANVAS_V3_COMMAND_ID_REUSED", "requestId already used with different input")
		}
		out.Replayed = true
		e = json.Unmarshal([]byte(effects), &out.Effects)
		return out, domainError(e)
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return out, domainError(e)
	}
	if int64(m["expectedRevision"].(float64)) != d.Revision {
		return out, canvasError(409, "CANVAS_V3_REVISION_CONFLICT", "Canvas revision has changed; refresh and edit again")
	}
	if d.Revision >= 9007199254740991 {
		return out, canvasInvalid("Revision limit reached")
	}
	undo, e := applyCommand(&d, m["command"], false)
	if e != nil {
		return out, e
	}
	refs, e := checkCanvasReferences(ctx, tx, p, d, undo)
	if e != nil {
		return out, e
	}
	d.Revision++
	out.Document = d
	out.AppliedRevision = d.Revision
	out.Effects = CanvasEffects{undo, []string{}}
	docJSON, _ := json.Marshal(d)
	effectJSON, _ := json.Marshal(out.Effects)
	if _, e = tx.ExecContext(ctx, `INSERT INTO canvas_documents VALUES(?,?) ON CONFLICT(project_id) DO UPDATE SET document_json=excluded.document_json`, p, string(docJSON)); e != nil {
		return out, domainError(e)
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO canvas_commands VALUES(?,?,?,?,?)`, p, key, string(canonical), d.Revision, string(effectJSON)); e != nil {
		return out, domainError(e)
	}
	// Retain only each receipt's undo references, and replace the current document refs.
	if _, e = tx.ExecContext(ctx, "DELETE FROM canvas_references WHERE project_id=? AND owner='document'", p); e != nil {
		return out, domainError(e)
	}
	rawDoc := any(nil)
	json.Unmarshal(docJSON, &rawDoc)
	for owner, owned := range map[string]map[string]map[string]bool{"document": references(rawDoc), "command:" + key: references(undo)} {
		for kind, ids := range owned {
			for id := range ids {
				if !refs[kind][id] {
					return out, canvasInvalid("Unvalidated reference")
				}
				if _, e = tx.ExecContext(ctx, "INSERT OR IGNORE INTO canvas_references VALUES(?,?,?,?)", p, owner, kind, id); e != nil {
					return out, domainError(e)
				}
			}
		}
	}
	return out, domainError(tx.Commit())
}
func checkCanvasReference(ctx context.Context, tx *sql.Tx, kind, id string) error {
	var n int
	var e error
	if kind == "projects" {
		e = tx.QueryRowContext(ctx, "SELECT (SELECT count(*) FROM canvas_commands WHERE project_id=?)+(SELECT count(*) FROM canvas_documents WHERE project_id=?)", id, id).Scan(&n)
	} else {
		e = tx.QueryRowContext(ctx, "SELECT count(*) FROM canvas_references WHERE kind=? AND object_id=?", kind, id).Scan(&n)
	}
	if e != nil {
		return e
	}
	if n > 0 {
		return protected()
	}
	return nil
}

func (c Canvas) Content(ctx context.Context, p string, input []byte) (map[string]any, error) {
	canonical, e := CanonicalJSON(input)
	if e != nil {
		return nil, canvasInvalid(e.Error())
	}
	var raw any
	json.Unmarshal(canonical, &raw)
	m, e := fields(raw, "compositionIds", "")
	if e != nil {
		return nil, e
	}
	ids, ok := m["compositionIds"].([]any)
	if !ok || len(ids) > 200 {
		return nil, canvasInvalid("Content batch exceeds 200")
	}
	seen := map[string]bool{}
	for _, v := range ids {
		if !canvasID(v) {
			return nil, canvasInvalid("Invalid composition id")
		}
		id := v.(string)
		if seen[id] {
			return nil, canvasInvalid("Duplicate composition id")
		}
		seen[id] = true
	}
	tx, e := c.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, domainError(e)
	}
	defer tx.Rollback()
	if e = requireProject(ctx, tx, p); e != nil {
		return nil, domainError(e)
	}
	items := []any{}
	for _, v := range ids {
		id := v.(string)
		item, e := compositionContent(ctx, tx, p, id)
		if e != nil {
			return nil, domainError(e)
		}
		items = append(items, item)
	}
	return map[string]any{"items": items}, nil
}
func compositionContent(ctx context.Context, tx *sql.Tx, p, id string) (any, error) {
	var name, typ, label string
	var revision int64
	e := tx.QueryRowContext(ctx, `SELECT c.name,c.type_id,coalesce(t.label,''),s.revision FROM compositions c JOIN canvas_composition_state s ON s.composition_id=c.id LEFT JOIN composition_types t ON t.id=c.type_id WHERE c.project_id=? AND c.id=?`, p, id).Scan(&name, &typ, &label, &revision)
	if errors.Is(e, sql.ErrNoRows) {
		return map[string]any{"compositionId": id, "state": "missing"}, nil
	}
	if e != nil {
		return nil, e
	}
	members := []any{}
	resources := map[string]any{}
	rids := map[string]bool{}
	rows, e := tx.QueryContext(ctx, `SELECT id,role,resource_id FROM composition_members WHERE composition_id=? ORDER BY id`, id)
	if e != nil {
		return nil, e
	}
	for rows.Next() {
		var mid int64
		var role, rid string
		if e = rows.Scan(&mid, &role, &rid); e != nil {
			rows.Close()
			return nil, e
		}
		members = append(members, map[string]any{"memberId": fmt.Sprint(mid), "role": role, "resource": rid})
		rids[rid] = true
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	// Core ownership can exist without a placement (direct M2 CRUD). Project it with
	// a stable support member identity; real composition_members remain authoritative.
	rows, e = tx.QueryContext(ctx, "SELECT id FROM resources WHERE composition_id=? ORDER BY id", id)
	if e != nil {
		return nil, e
	}
	for rows.Next() {
		var rid string
		if e = rows.Scan(&rid); e != nil {
			rows.Close()
			return nil, e
		}
		if !rids[rid] {
			members = append(members, map[string]any{"memberId": "owned:" + rid, "role": "", "resource": rid})
			rids[rid] = true
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	keys := []string{}
	for rid := range rids {
		keys = append(keys, rid)
	}
	sort.Strings(keys)
	for _, rid := range keys {
		r, e := resourceContent(ctx, tx, p, rid)
		if e != nil {
			return nil, e
		}
		resources[rid] = r
	}
	return map[string]any{"compositionId": id, "state": "ready", "revision": revision, "title": name, "typeId": typ, "typeLabel": label, "members": members, "taskOutputs": []any{}, "protected": false, "resources": resources}, nil
}
func resourceContent(ctx context.Context, tx *sql.Tx, p, id string) (any, error) {
	r := map[string]any{"resourceId": id, "name": "", "kind": "file", "currentVersionId": nil, "availability": "missing", "versions": []any{}}
	var name string
	var current sql.NullString
	e := tx.QueryRowContext(ctx, `SELECT coalesce(r.description,''),s.current_version_id FROM resources r JOIN compositions c ON c.id=r.composition_id JOIN canvas_resource_state s ON s.resource_id=r.id WHERE r.id=? AND c.project_id=?`, id, p).Scan(&name, &current)
	if errors.Is(e, sql.ErrNoRows) {
		return r, nil
	}
	if e != nil {
		return nil, e
	}
	r["name"] = name
	r["availability"] = "unavailable"
	if current.Valid {
		r["currentVersionId"] = current.String
	}
	rows, e := tx.QueryContext(ctx, `SELECT v.id,s.ordinal,coalesce(a.status,'missing'),coalesce(a.mime_type,''),coalesce(a.name,'') FROM versions v JOIN canvas_version_state s ON s.version_id=v.id LEFT JOIN assets a ON a.id=v.asset_id WHERE v.resource_id=? ORDER BY s.ordinal`, id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	versions := []any{}
	for rows.Next() {
		var vid, status, mime, assetName string
		var ordinal int64
		if e = rows.Scan(&vid, &ordinal, &status, &mime, &assetName); e != nil {
			return nil, e
		}
		availability := "unavailable"
		if status == "active" {
			availability = "available"
		}
		versions = append(versions, map[string]any{"versionId": vid, "ordinal": ordinal, "availability": availability})
		if vid == current.String {
			r["availability"] = availability
			if name == "" {
				r["name"] = assetName
			}
			switch {
			case strings.HasPrefix(mime, "image/"):
				r["kind"] = "image"
			case strings.HasPrefix(mime, "video/"):
				r["kind"] = "video"
			case strings.HasPrefix(mime, "audio/"):
				r["kind"] = "audio"
			case strings.HasPrefix(mime, "text/"):
				r["kind"] = "text"
			}
		}
	}
	r["versions"] = versions
	return r, rows.Err()
}
