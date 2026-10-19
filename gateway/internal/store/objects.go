package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Mutation struct {
	RequestID string          `json:"requestId" validate:"required,max=128"`
	Action    string          `json:"action" validate:"required,oneof=create update delete"`
	ID        string          `json:"id,omitempty"`
	Value     json.RawMessage `json:"value,omitempty"`
}

// Empty id/null value have no mutation meaning. Reject them instead of letting
// omitempty collapse a distinct accepted HTTP input onto an existing receipt.
func (m *Mutation) UnmarshalJSON(raw []byte) error {
	type dto Mutation
	var value dto
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if _, exists := fields["id"]; exists && value.ID == "" {
		return invalid("id must be omitted or nonempty")
	}
	if field, exists := fields["value"]; exists && string(field) == "null" {
		return invalid("value must be omitted or an object")
	}
	*m = Mutation(value)
	return nil
}

type Coverage struct {
	Total      int  `json:"total"`
	NextOffset int  `json:"next_offset"`
	Exhausted  bool `json:"exhausted"`
}
type Page struct {
	Items    []json.RawMessage `json:"items"`
	Coverage Coverage          `json:"coverage"`
}
type CompositionType struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Label     string `json:"label"`
}
type Composition struct {
	ID         string  `json:"id"`
	ProjectID  string  `json:"project_id"`
	TypeID     string  `json:"type_id"`
	Name       string  `json:"name"`
	Attributes *string `json:"attributes"`
}
type Resource struct {
	ID            string  `json:"id"`
	CompositionID string  `json:"composition_id"`
	Description   *string `json:"description"`
}
type Version struct {
	ID              string  `json:"id"`
	ResourceID      string  `json:"resource_id"`
	AssetID         *string `json:"asset_id"`
	SourceVersionID *string `json:"source_version_id"`
	Origin          string  `json:"origin"`
	CreatedAt       int64   `json:"created_at"`
}
type Asset struct {
	ID         string  `json:"id"`
	Path       string  `json:"path"`
	Status     string  `json:"status"`
	Size       int64   `json:"size"`
	MtimeMS    int64   `json:"mtime_ms"`
	DevID      *int64  `json:"dev_id"`
	Inode      *int64  `json:"inode"`
	QuickHash  *string `json:"quick_hash"`
	FullHash   *string `json:"full_hash"`
	Width      *int64  `json:"width"`
	Height     *int64  `json:"height"`
	DurationMS *int64  `json:"duration_ms"`
	MIMEType   *string `json:"mime_type"`
	Name       string  `json:"name"`
	Metadata   *string `json:"metadata"`
	CreatedAt  int64   `json:"created_at"`
	UpdatedAt  int64   `json:"updated_at"`
}
type typeValue struct {
	Label string `json:"label" validate:"required,max=256"`
}
type compositionValue struct {
	TypeID     string  `json:"type_id" validate:"required,max=128"`
	Name       string  `json:"name" validate:"required,max=256"`
	Attributes *string `json:"attributes"`
}
type compositionUpdate struct {
	Name       *string `json:"name"`
	Attributes *string `json:"attributes"`
}
type resourceValue struct {
	CompositionID string  `json:"composition_id" validate:"required,max=128"`
	Description   *string `json:"description"`
}
type resourceUpdate struct {
	Description *string `json:"description"`
}
type versionValue struct {
	ResourceID      string  `json:"resource_id" validate:"required,max=128"`
	AssetID         *string `json:"asset_id"`
	SourceVersionID *string `json:"source_version_id"`
	Origin          string  `json:"origin" validate:"omitempty,oneof=agent user"`
}
type assetUpdate struct {
	Name     *string `json:"name"`
	Metadata *string `json:"metadata"`
}

// Queries are fixed SQL, never fields or table names supplied by the caller.
func objectQuery(kind string) (columns, from, project, id string, err error) {
	switch kind {
	case "projects":
		return "o.id,o.name,o.created_at", "projects o", "o.id", "o.id", nil
	case "composition_types":
		return "o.id,o.project_id,o.label", "composition_types o", "o.project_id", "o.id", nil
	case "compositions":
		return "o.id,o.project_id,o.type_id,o.name,o.attributes", "compositions o", "o.project_id", "o.id", nil
	case "resources":
		return "o.id,o.composition_id,o.description", "resources o JOIN compositions c ON c.id=o.composition_id", "c.project_id", "o.id", nil
	case "versions":
		return "o.id,o.resource_id,o.asset_id,o.source_version_id,o.origin,o.created_at", "versions o JOIN resources r ON r.id=o.resource_id JOIN compositions c ON c.id=r.composition_id", "c.project_id", "o.id", nil
	case "assets":
		return "o.id,o.path,o.status,o.size,o.mtime_ms,o.dev_id,o.inode,o.quick_hash,o.full_hash,o.width,o.height,o.duration_ms,o.mime_type,o.name,o.metadata,o.created_at,o.updated_at", "assets o JOIN asset_projects a ON a.asset_id=o.id", "a.project_id", "o.id", nil
	default:
		return "", "", "", "", invalid("Unknown object kind")
	}
}
func scanObjectRow(kind string, row interface{ Scan(...any) error }) (json.RawMessage, error) {
	var value any
	var err error
	switch kind {
	case "projects":
		v := Project{}
		err = row.Scan(&v.ID, &v.Name, &v.CreatedAt)
		value = v
	case "composition_types":
		v := CompositionType{}
		err = row.Scan(&v.ID, &v.ProjectID, &v.Label)
		value = v
	case "compositions":
		v := Composition{}
		err = row.Scan(&v.ID, &v.ProjectID, &v.TypeID, &v.Name, &v.Attributes)
		value = v
	case "resources":
		v := Resource{}
		err = row.Scan(&v.ID, &v.CompositionID, &v.Description)
		value = v
	case "versions":
		v := Version{}
		err = row.Scan(&v.ID, &v.ResourceID, &v.AssetID, &v.SourceVersionID, &v.Origin, &v.CreatedAt)
		value = v
	case "assets":
		v := Asset{}
		err = row.Scan(&v.ID, &v.Path, &v.Status, &v.Size, &v.MtimeMS, &v.DevID, &v.Inode, &v.QuickHash, &v.FullHash, &v.Width, &v.Height, &v.DurationMS, &v.MIMEType, &v.Name, &v.Metadata, &v.CreatedAt, &v.UpdatedAt)
		value = v
	default:
		return nil, invalid("Unknown object kind")
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(value)
}
func validID(id string) bool { return len(id) > 0 && len(id) <= 128 && identifier.MatchString(id) }
func requireProject(ctx context.Context, tx *sql.Tx, projectID string) error {
	if !validID(projectID) || projectID == "_app" {
		return invalid("Invalid project_id")
	}
	var id string
	return tx.QueryRowContext(ctx, `SELECT id FROM projects WHERE id=?`, projectID).Scan(&id)
}
func getObject(ctx context.Context, tx *sql.Tx, projectID, kind, id string) (json.RawMessage, error) {
	if !validID(id) {
		return nil, invalid("Invalid object id")
	}
	cols, from, project, key, err := objectQuery(kind)
	if err != nil {
		return nil, err
	}
	return scanObjectRow(kind, tx.QueryRowContext(ctx, "SELECT "+cols+" FROM "+from+" WHERE "+project+"=? AND "+key+"=?", projectID, id))
}
func (s Objects) Get(ctx context.Context, projectID, kind, id string) (json.RawMessage, error) {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, domainError(err)
	}
	defer tx.Rollback()
	if err = requireProject(ctx, tx, projectID); err != nil {
		return nil, domainError(err)
	}
	out, err := getObject(ctx, tx, projectID, kind, id)
	return out, domainError(err)
}
func (s Objects) List(ctx context.Context, projectID, kind string, offset, limit int) (Page, error) {
	page := Page{Items: []json.RawMessage{}}
	if limit == 0 {
		limit = 50
	}
	if offset < 0 || limit < 1 || limit > 200 {
		return page, invalid("offset must be nonnegative and limit must be 1–200")
	}
	cols, from, project, id, err := objectQuery(kind)
	if err != nil {
		return page, err
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return page, domainError(err)
	}
	defer tx.Rollback()
	where := " WHERE " + project + "=?"
	args := []any{projectID}
	if kind == "projects" && projectID == "_app" {
		where = ""
		args = nil
	} else if err = requireProject(ctx, tx, projectID); err != nil {
		return page, domainError(err)
	}
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM "+from+where, args...).Scan(&page.Coverage.Total); err != nil {
		return page, domainError(err)
	}
	args = append(args, limit, offset)
	rows, err := tx.QueryContext(ctx, "SELECT "+cols+" FROM "+from+where+" ORDER BY "+id+" LIMIT ? OFFSET ?", args...)
	if err != nil {
		return page, domainError(err)
	}
	defer rows.Close()
	for rows.Next() {
		item, e := scanObjectRow(kind, rows)
		if e != nil {
			return page, domainError(e)
		}
		page.Items = append(page.Items, item)
	}
	if err = rows.Err(); err != nil {
		return page, domainError(err)
	}
	page.Coverage.NextOffset = offset + len(page.Items)
	page.Coverage.Exhausted = page.Coverage.NextOffset >= page.Coverage.Total
	return page, nil
}

func (s Objects) Mutate(ctx context.Context, projectID, kind string, input Mutation) (json.RawMessage, error) {
	if _, _, _, _, err := objectQuery(kind); err != nil {
		return nil, err
	}
	if err := validate.Struct(input); err != nil {
		return nil, invalid(err.Error())
	}
	scope := Scope{ProjectID: projectID, Operation: kind + "." + input.Action, RequestID: input.RequestID}
	if err := scope.Validate(); err != nil {
		return nil, invalid(err.Error())
	}
	if input.Action == "create" {
		if input.ID != "" {
			return nil, invalid("create must not supply id")
		}
	} else if !validID(input.ID) {
		return nil, invalid("update/delete requires id")
	}
	if input.Action == "delete" {
		if len(input.Value) > 0 {
			canonical, err := CanonicalJSON(input.Value)
			if err != nil || string(canonical) != "{}" {
				return nil, invalid("delete accepts only an omitted or empty object value")
			}
		}
	} else if _, err := CanonicalJSON(input.Value); err != nil {
		return nil, invalid("value must be one JSON object")
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, invalid(err.Error())
	}
	out, err := WriteRequest(ctx, s.DB, scope, raw, func(tx *sql.Tx) ([]byte, error) {
		if kind == "projects" && input.Action == "create" {
			if projectID != "_app" {
				return nil, invalid("Project creation requires application scope")
			}
			return createProject(ctx, tx, input.Value)
		}
		if err := requireProject(ctx, tx, projectID); err != nil {
			return nil, err
		}
		if input.Action == "create" {
			return createObject(ctx, tx, projectID, kind, input.Value)
		}
		if _, err := getObject(ctx, tx, projectID, kind, input.ID); err != nil {
			return nil, err
		}
		if input.Action == "delete" {
			return deleteObject(ctx, tx, kind, input.ID)
		}
		return updateObject(ctx, tx, projectID, kind, input.ID, input.Value)
	})
	return out, domainError(err)
}
func jsonText(value *string) error {
	if value == nil {
		return nil
	}
	if _, err := CanonicalJSON([]byte(*value)); err != nil {
		return invalid("attributes/metadata must contain a JSON object")
	}
	return nil
}
func createObject(ctx context.Context, tx *sql.Tx, p, kind string, raw json.RawMessage) (json.RawMessage, error) {
	id := uuid.NewString()
	var err error
	switch kind {
	case "composition_types":
		v, e := DecodeValue[typeValue](raw)
		if e != nil {
			return nil, invalid(e.Error())
		}
		if e = CheckTaskTargetReservation(ctx, tx, p, "", v.Label, ""); e != nil {
			return nil, e
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO composition_types(id,project_id,label) VALUES(?,?,?) ON CONFLICT(project_id,label) DO NOTHING`, id, p, v.Label)
		if err == nil {
			err = tx.QueryRowContext(ctx, `SELECT id FROM composition_types WHERE project_id=? AND label=?`, p, v.Label).Scan(&id)
		}
	case "compositions":
		v, e := DecodeValue[compositionValue](raw)
		if e != nil {
			return nil, invalid(e.Error())
		}
		if e = CheckTaskTargetReservation(ctx, tx, p, v.TypeID, "", v.Name); e != nil {
			return nil, e
		}
		if e = jsonText(v.Attributes); e != nil {
			return nil, e
		}
		if _, e = getObject(ctx, tx, p, "composition_types", v.TypeID); e != nil {
			return nil, e
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO compositions(id,project_id,type_id,name,attributes) VALUES(?,?,?,?,?)`, id, p, v.TypeID, v.Name, v.Attributes)
	case "resources":
		v, e := DecodeValue[resourceValue](raw)
		if e != nil {
			return nil, invalid(e.Error())
		}
		if _, e = getObject(ctx, tx, p, "compositions", v.CompositionID); e != nil {
			return nil, e
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO resources(id,composition_id,description) VALUES(?,?,?)`, id, v.CompositionID, v.Description)
	case "versions":
		v, e := DecodeValue[versionValue](raw)
		if e != nil {
			return nil, invalid(e.Error())
		}
		if v.Origin == "" {
			var fields map[string]json.RawMessage
			json.Unmarshal(raw, &fields)
			if _, present := fields["origin"]; present {
				return nil, invalid("origin must be agent or user when present")
			}
		}
		if _, e = getObject(ctx, tx, p, "resources", v.ResourceID); e != nil {
			return nil, e
		}
		for kind, ref := range map[string]*string{"assets": v.AssetID, "versions": v.SourceVersionID} {
			if ref != nil {
				if _, e = getObject(ctx, tx, p, kind, *ref); e != nil {
					return nil, e
				}
			}
		}
		if v.AssetID != nil {
			if e = CheckAssetFilePending(ctx, tx, *v.AssetID); e != nil {
				return nil, e
			}
			var status string
			if e = tx.QueryRowContext(ctx, `SELECT status FROM assets WHERE id=?`, *v.AssetID).Scan(&status); e != nil {
				return nil, e
			}
			if status != "active" {
				return nil, invalid("Asset file is missing")
			}
		}
		if v.Origin == "" {
			v.Origin = "agent"
		}
		if e = CheckCreativeVersion(ctx, tx, v.ResourceID, id); e != nil {
			return nil, e
		}
		// A revision keeps resource_id; derivation may reference another resource's version in this project.
		_, err = tx.ExecContext(ctx, `INSERT INTO versions(id,resource_id,asset_id,source_version_id,origin,created_at) VALUES(?,?,?,?,?,?)`, id, v.ResourceID, v.AssetID, v.SourceVersionID, v.Origin, time.Now().UnixMilli())
	case "assets":
		return nil, invalid("Asset creation requires trusted file registration through /api/upload and /api/upload/commit")
	default:
		return nil, invalid("Unsupported create")
	}
	if err != nil {
		return nil, err
	}
	return getObject(ctx, tx, p, kind, id)
}

func updateObject(ctx context.Context, tx *sql.Tx, p, kind, id string, raw json.RawMessage) (json.RawMessage, error) {
	if kind == "versions" {
		return nil, &DomainError{409, "invalid_input", "Versions are immutable; create a new version on the same resource"}
	}
	var fields map[string]json.RawMessage
	json.Unmarshal(raw, &fields)
	if len(fields) == 0 {
		return nil, invalid("update requires at least one field")
	}
	var err error
	switch kind {
	case "projects":
		v, e := DecodeValue[projectValue](raw)
		if e != nil {
			return nil, invalid(e.Error())
		}
		_, err = tx.ExecContext(ctx, `UPDATE projects SET name=? WHERE id=?`, v.Name, id)
	case "composition_types":
		v, e := DecodeValue[typeValue](raw)
		if e != nil {
			return nil, invalid(e.Error())
		}
		var other string
		e = tx.QueryRowContext(ctx, `SELECT id FROM composition_types WHERE project_id=? AND label=? AND id<>?`, p, v.Label, id).Scan(&other)
		if e == nil {
			return nil, &DomainError{409, "invalid_input", "Type label already exists"}
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return nil, e
		}
		if e = checkDeliveryRename(ctx, tx, kind, id, v.Label); e != nil {
			return nil, e
		}
		_, err = tx.ExecContext(ctx, `UPDATE composition_types SET label=? WHERE id=?`, v.Label, id)
	case "compositions":
		v, e := DecodeValue[compositionUpdate](raw)
		if e != nil {
			return nil, invalid(e.Error())
		}
		if e = jsonText(v.Attributes); e != nil {
			return nil, e
		}
		if _, ok := fields["name"]; ok {
			if v.Name == nil || len(*v.Name) == 0 || len([]rune(*v.Name)) > 256 {
				return nil, invalid("name must contain 1–256 characters")
			}
			if e = checkDeliveryRename(ctx, tx, kind, id, *v.Name); e != nil {
				return nil, e
			}
			if _, err = tx.ExecContext(ctx, `UPDATE compositions SET name=? WHERE id=?`, v.Name, id); err != nil {
				return nil, err
			}
		}
		if _, ok := fields["attributes"]; ok {
			_, err = tx.ExecContext(ctx, `UPDATE compositions SET attributes=? WHERE id=?`, v.Attributes, id)
		}
	case "resources":
		v, e := DecodeValue[resourceUpdate](raw)
		if e != nil {
			return nil, invalid(e.Error())
		}
		_, err = tx.ExecContext(ctx, `UPDATE resources SET description=? WHERE id=?`, v.Description, id)
	case "assets":
		if e := CheckAssetFilePending(ctx, tx, id); e != nil {
			return nil, e
		}
		var p, path string
		if e := tx.QueryRowContext(ctx, "SELECT ap.project_id,a.path FROM assets a JOIN asset_projects ap ON ap.asset_id=a.id WHERE a.id=?", id).Scan(&p, &path); e != nil {
			return nil, e
		}
		if e := CheckDeliveryPaths(ctx, tx, p, "", path); e != nil {
			return nil, e
		}
		v, e := DecodeValue[assetUpdate](raw)
		if e != nil {
			return nil, invalid(e.Error())
		}
		if e = jsonText(v.Metadata); e != nil {
			return nil, e
		}
		if _, ok := fields["name"]; ok {
			if v.Name == nil || len(*v.Name) == 0 || len([]rune(*v.Name)) > 256 {
				return nil, invalid("name must contain 1–256 characters")
			}
			if _, err = tx.ExecContext(ctx, `UPDATE assets SET name=? WHERE id=?`, v.Name, id); err != nil {
				return nil, err
			}
		}
		if _, ok := fields["metadata"]; ok {
			if _, err = tx.ExecContext(ctx, `UPDATE assets SET metadata=? WHERE id=?`, v.Metadata, id); err != nil {
				return nil, err
			}
		}
		_, err = tx.ExecContext(ctx, `UPDATE assets SET updated_at=? WHERE id=?`, time.Now().UnixMilli(), id)
	default:
		return nil, invalid("Unsupported update")
	}
	if err != nil {
		return nil, err
	}
	return getObject(ctx, tx, p, kind, id)
}

func checkPipelineReference(ctx context.Context, tx *sql.Tx, kind, id string) error {
	var n int
	var err error
	switch kind {
	case "projects":
		err = tx.QueryRowContext(ctx, `SELECT count(*) FROM pipelines WHERE project_id=?`, id).Scan(&n)
	case "compositions":
		err = tx.QueryRowContext(ctx, `SELECT count(*) FROM resources r WHERE r.composition_id=? AND (EXISTS(SELECT 1 FROM pipelines WHERE resource_id=r.id) OR EXISTS(SELECT 1 FROM creative_artifacts WHERE resource_id=r.id) OR EXISTS(SELECT 1 FROM prompt_revisions WHERE resource_id=r.id))`, id).Scan(&n)
	case "resources":
		err = tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM pipelines WHERE resource_id=?)+(SELECT count(*) FROM creative_artifacts WHERE resource_id=?)+(SELECT count(*) FROM prompt_revisions WHERE resource_id=?)`, id, id, id).Scan(&n)
	case "versions":
		err = tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM storyboard_revisions WHERE document_version_id=?)+(SELECT count(*) FROM creative_artifacts WHERE document_version_id=? OR source_version_id=?)+(SELECT count(*) FROM prompt_revisions WHERE document_version_id=? OR source_version_id=?)+(SELECT count(*) FROM prompt_contracts WHERE document_version_id=?)+(SELECT count(*) FROM approvals WHERE document_version_id=?)+(SELECT count(*) FROM version_dependencies WHERE version_id=? OR source_version_id=?)+(SELECT count(*) FROM prompt_contracts c, json_each(CASE WHEN json_type(c.content_json,'$.references')='array' THEN json_extract(c.content_json,'$.references') ELSE '[]' END) j WHERE json_extract(j.value,'$.versionId')=?)`, id, id, id, id, id, id, id, id, id, id).Scan(&n)
	case "assets":
		err = tx.QueryRowContext(ctx, `SELECT count(*) FROM prompt_contracts c, json_each(CASE WHEN json_type(c.content_json,'$.references')='array' THEN json_extract(c.content_json,'$.references') ELSE '[]' END) j WHERE json_extract(j.value,'$.assetId')=?`, id).Scan(&n)
	default:
		return nil
	}
	if err != nil {
		return err
	}
	if n > 0 {
		return protected()
	}
	return nil
}

func deleteObject(ctx context.Context, tx *sql.Tx, kind, id string) (json.RawMessage, error) {
	if e := checkTaskReference(ctx, tx, kind, id); e != nil {
		return nil, e
	}
	if e := checkCanvasReference(ctx, tx, kind, id); e != nil {
		return nil, e
	}
	if e := checkDeliveryReference(ctx, tx, kind, id); e != nil {
		return nil, e
	}
	if e := checkPipelineReference(ctx, tx, kind, id); e != nil {
		return nil, e
	}
	var query, statement string
	var args []any
	switch kind {
	case "projects":
		query = `SELECT (SELECT count(*) FROM composition_types WHERE project_id=?)+(SELECT count(*) FROM compositions WHERE project_id=?)+(SELECT count(*) FROM asset_projects WHERE project_id=?)+(SELECT count(*) FROM file_operations WHERE project_id=?)+(SELECT count(*) FROM chat_sessions WHERE project_id=?)+(SELECT count(*) FROM project_rules WHERE project_id=?)+(SELECT count(*) FROM questions WHERE project_id=?)`
		args = []any{id, id, id, id, id, id, id}
		statement = `DELETE FROM projects WHERE id=?`
	case "composition_types":
		query = `SELECT count(*) FROM compositions WHERE type_id=?`
		args = []any{id}
		statement = `DELETE FROM composition_types WHERE id=?`
	case "compositions":
		query = `SELECT (SELECT count(*) FROM resources WHERE composition_id=?)+(SELECT count(*) FROM composition_members WHERE composition_id=?)`
		args = []any{id, id}
		statement = `DELETE FROM compositions WHERE id=?`
	case "resources":
		query = `SELECT (SELECT count(*) FROM versions WHERE resource_id=?)+(SELECT count(*) FROM composition_members WHERE resource_id=?)`
		args = []any{id, id}
		statement = `DELETE FROM resources WHERE id=?`
	case "versions":
		query = `SELECT count(*) FROM versions WHERE source_version_id=?`
		args = []any{id}
		statement = `DELETE FROM versions WHERE id=?`
	case "assets":
		if e := CheckAssetFilePending(ctx, tx, id); e != nil {
			return nil, e
		}
		query = `SELECT count(*) FROM versions WHERE asset_id=?`
		args = []any{id}
		statement = `UPDATE assets SET status='missing',updated_at=? WHERE id=?`
	default:
		return nil, invalid("Unsupported delete")
	}
	var n int
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		return nil, err
	}
	if n > 0 {
		return nil, protected()
	}
	args = []any{id}
	if kind == "assets" {
		args = []any{time.Now().UnixMilli(), id}
	}
	if _, err := tx.ExecContext(ctx, statement, args...); err != nil {
		return nil, err
	}
	return json.RawMessage(fmt.Sprintf(`{"id":%q,"deleted":true}`, id)), nil
}

// CheckAssetFilePending is SQL-only and must run inside the same transaction as
// creation of a Version or an asset metadata/delete mutation.
func CheckAssetFilePending(ctx context.Context, tx *sql.Tx, id string) error {
	var p, v string
	if e := tx.QueryRowContext(ctx, "SELECT ap.project_id,a.path FROM assets a JOIN asset_projects ap ON ap.asset_id=a.id WHERE a.id=?", id).Scan(&p, &v); e != nil {
		return e
	}
	return CheckFilePending(ctx, tx, p, v)
}

// CheckFilePending protects all source and destination paths in the caller's transaction.
func CheckFilePending(ctx context.Context, tx *sql.Tx, p string, paths ...string) error {
	rows, e := tx.QueryContext(ctx, "SELECT source FROM file_operations WHERE project_id=? AND stage='prepared'", p)
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var raw sql.NullString
		if e = rows.Scan(&raw); e != nil {
			return e
		}
		if !raw.Valid {
			continue
		}
		var plans []struct {
			Source string `json:"source"`
			Target string `json:"target"`
		}
		if e = json.Unmarshal([]byte(raw.String), &plans); e != nil {
			return e
		}
		for _, m := range plans {
			for _, v := range paths {
				for _, x := range []string{m.Source, m.Target} {
					a, b := strings.ToLower(v), strings.ToLower(x)
					if b != "" && (a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")) {
						return ErrRequestPending
					}
				}
			}
		}
	}
	return rows.Err()
}

// Every Delivery path shares this guard. Untyped revisions cannot silently replace
// an authoritative creative artifact; generic unrelated Resources are unaffected.
func CheckCreativeVersion(ctx context.Context, tx *sql.Tx, resource, version string) error {
	var n int
	e := tx.QueryRowContext(ctx, `SELECT (EXISTS(SELECT 1 FROM creative_artifacts WHERE resource_id=?) OR EXISTS(SELECT 1 FROM pipelines WHERE resource_id=?) OR EXISTS(SELECT 1 FROM prompt_revisions WHERE resource_id=?)) AND NOT (EXISTS(SELECT 1 FROM creative_artifacts WHERE document_version_id=?) OR EXISTS(SELECT 1 FROM storyboard_revisions WHERE document_version_id=?) OR EXISTS(SELECT 1 FROM prompt_revisions WHERE document_version_id=?))`, resource, resource, resource, version, version, version).Scan(&n)
	if e != nil {
		return e
	}
	if n != 0 {
		return invalid("Registered creative Resources require typed document revisions")
	}
	return nil
}
