package delivery

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/workspace"
)

// ponytail: one process-wide lock serializes delivery filesystem phases across
// trusted user/agent service instances; multi-process gateways require a DB lease.
var deliveryMu sync.Mutex

type Service struct {
	db        *sql.DB
	workspace *workspace.Workspace
	origin    string
	fault     func(string) error
}

func NewService(db *sql.DB, w *workspace.Workspace, origin string) *Service {
	return &Service{db: db, workspace: w, origin: origin}
}

type plannedSource struct {
	Source                                     Source
	Asset                                      store.Asset
	Existing                                   bool
	ResourceID, VersionID, BaseVersionID, Name string
}
type plan struct {
	CompositionID, TypeID, TypeLabel, Name string
	NewType, NewComposition                bool
	Sources                                []plannedSource
	Placements                             []Placement
}
type journal struct {
	Input, Stage, Status, Origin string
	Plan                         *plan
	Receipt                      Receipt
}

func invalid(message string) error {
	return &store.DomainError{Status: 400, Code: "invalid_input", Message: message}
}
func missing(message string) error {
	return &store.DomainError{Status: 404, Code: "not_found", Message: message}
}
func result(status string, e error) Receipt {
	r := Receipt{Delivery: Result{Status: status, Versions: []Version{}}}
	if e != nil {
		r.Delivery.Problem = &Problem{Code: "upstream", Message: "Delivery interrupted; retry the same requestId and input"}
		var d *store.DomainError
		if errors.As(e, &d) {
			r.Delivery.Problem = &Problem{Code: d.Code, Message: d.Message}
		} else if errors.Is(e, store.ErrRequestPending) {
			r.Delivery.Problem = &Problem{Code: "waiting_user", Message: "A pending file or delivery operation must finish first"}
		}
	}
	return r
}
func inputJSON(in Request) ([]byte, error) {
	b := in.raw
	if b != nil {
		original, e := store.DecodeValue[Request](b)
		if e != nil {
			return nil, e
		}
		before, _ := json.Marshal(original)
		after, _ := json.Marshal(in)
		if string(before) != string(after) {
			return nil, invalid("Decoded delivery input was modified; construct a new Request")
		}
	}
	if b == nil {
		var e error
		b, e = json.Marshal(in)
		if e != nil {
			return nil, e
		}
	}
	return store.CanonicalJSON(b)
}
func (s *Service) load(ctx context.Context, p, key string) (*journal, error) {
	j := &journal{}
	var pl, receipt sql.NullString
	e := s.db.QueryRowContext(ctx, "SELECT input_json,stage,status,origin,plan_json,receipt FROM deliveries WHERE project_id=? AND request_id=?", p, key).Scan(&j.Input, &j.Stage, &j.Status, &j.Origin, &pl, &receipt)
	if e != nil {
		return nil, e
	}
	if pl.Valid {
		if e = json.Unmarshal([]byte(pl.String), &j.Plan); e != nil {
			return nil, e
		}
	}
	if receipt.Valid {
		e = json.Unmarshal([]byte(receipt.String), &j.Receipt)
	}
	return j, e
}
func (s *Service) Deliver(ctx context.Context, p string, in Request) (Receipt, error) {
	deliveryMu.Lock()
	defer deliveryMu.Unlock()
	if e := (store.Scope{ProjectID: p, RequestID: in.RequestID, Operation: "deliver"}).Validate(); e != nil || p == "_app" {
		return Receipt{}, invalid("Invalid project_id or requestId")
	}
	if s.origin != "user" && s.origin != "agent" {
		return Receipt{}, invalid("Invalid trusted delivery origin")
	}
	b, e := inputJSON(in)
	if e != nil {
		return Receipt{}, invalid(e.Error())
	}
	j, e := s.load(ctx, p, in.RequestID)
	if e == nil {
		if j.Input != string(b) {
			return Receipt{}, store.ErrRequestConflict
		}
		if j.Status != "unknown" {
			return j.Receipt, nil
		}
	} else if !errors.Is(e, sql.ErrNoRows) {
		return Receipt{}, e
	} else {
		j = &journal{Input: string(b), Origin: s.origin}
	}
	return s.run(ctx, p, in, j)
}
func (s *Service) Recover(ctx context.Context, p, key string) (Receipt, error) {
	deliveryMu.Lock()
	defer deliveryMu.Unlock()
	if e := (store.Scope{ProjectID: p, RequestID: key, Operation: "deliver"}).Validate(); e != nil || p == "_app" {
		return Receipt{}, invalid("Invalid project_id or requestId")
	}
	j, e := s.load(ctx, p, key)
	if errors.Is(e, sql.ErrNoRows) {
		return Receipt{}, missing("Delivery not found")
	}
	if e != nil {
		return Receipt{}, e
	}
	if j.Status != "unknown" {
		return j.Receipt, nil
	}
	in, e := store.DecodeValue[Request]([]byte(j.Input))
	if e != nil {
		return Receipt{}, e
	}
	return s.run(ctx, p, in, j)
}
func (s *Service) run(ctx context.Context, p string, in Request, j *journal) (Receipt, error) {
	var e error
	if j.Plan == nil {
		e = s.prepare(ctx, p, in, j)
		if e != nil {
			return s.failed(ctx, p, in.RequestID, j, e)
		}
	}
	if s.fault != nil {
		if e = s.fault("prepare"); e != nil {
			return result("unknown", e), nil
		}
	}
	for _, src := range j.Plan.Sources {
		if e = s.workspace.ArchiveDelivery(p, src.Source.Path, src.Asset.Path, src.Asset); e != nil {
			return s.failed(ctx, p, in.RequestID, j, e)
		}
		_, e = s.db.ExecContext(ctx, "UPDATE delivery_sources SET stage='archived' WHERE project_id=? AND request_id=? AND source_key=?", p, in.RequestID, src.Source.Key)
		if e != nil {
			return result("unknown", e), nil
		}
		if s.fault != nil {
			if e = s.fault("archive"); e != nil {
				return result("unknown", e), nil
			}
		}
	}
	for _, src := range j.Plan.Sources {
		if e = s.workspace.VerifyDelivery(p, src.Asset.Path, src.Asset); e != nil {
			return s.failed(ctx, p, in.RequestID, j, e)
		}
	}
	if s.fault != nil {
		if e = s.fault("before_commit"); e != nil {
			return result("unknown", e), nil
		}
	}
	r, e := s.commit(ctx, p, in.RequestID, j)
	if e != nil {
		return s.failed(ctx, p, in.RequestID, j, e)
	}
	if s.fault != nil {
		if e = s.fault("after_commit"); e != nil {
			return result("unknown", e), nil
		}
	}
	return r, nil
}
func (s *Service) failed(ctx context.Context, p, key string, j *journal, e error) (Receipt, error) {
	status := "unknown"
	var d *store.DomainError
	if errors.As(e, &d) {
		status = "blocked"
	} else if errors.Is(e, os.ErrNotExist) {
		status = "blocked"
		e = missing("Delivery source or archive not found")
	}
	r := result(status, e)
	raw, _ := json.Marshal(r)
	// Keep immutable frozen input/origin even when a prepare attempt could not proceed.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result("unknown", err), nil
	}
	defer tx.Rollback()
	var n int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM projects WHERE id=?", p).Scan(&n); err != nil {
		return result("unknown", err), nil
	}
	if n == 0 {
		return Receipt{}, missing("Project not found")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO deliveries(project_id,request_id,input_json,stage,status,origin,receipt) VALUES(?,?,?,'waiting',?,?,?) ON CONFLICT(project_id,request_id) DO UPDATE SET status=excluded.status,receipt=excluded.receipt`, p, key, j.Input, status, j.Origin, string(raw))
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		return result("unknown", err), nil
	}
	return r, nil
}
func validate(in Request) error {
	if len(in.Sources) < 1 || len(in.Sources) > 100 || len(in.Output.Resources) != len(in.Sources) {
		return invalid("Delivery requires 1–100 sources and one resource per source")
	}
	keys := map[string]bool{}
	for _, v := range in.Sources {
		if v.Key == "" || len(v.Key) > 128 || keys[v.Key] || (v.Path == "") == (v.TaskOutputKey == "") || len(v.Path) > 4096 {
			return invalid("Sources require unique keys and exactly one path or taskOutputKey")
		}
		keys[v.Key] = true
	}
	used := map[string]bool{}
	for _, v := range in.Output.Resources {
		if !keys[v.Key] || used[v.Key] || v.Name == "" || utf8.RuneCountInString(v.Name) > 512 {
			return invalid("Resource keys must equal source keys and names must be 1–512 characters")
		}
		used[v.Key] = true
		if v.Mode != "create" && v.Mode != "new_version" || v.Mode == "create" && v.BaseVersionID != "" || v.Mode == "new_version" && v.BaseVersionID == "" {
			return invalid("create forbids baseVersionId; new_version requires it")
		}
	}
	c := in.Output.Composition
	if e := validatePlacements(c.Placements); e != nil {
		return e
	}
	if c.Name == "" || utf8.RuneCountInString(c.Name) > 256 || (c.Type.ID == "" && c.Type.Label == "") || utf8.RuneCountInString(c.Type.Label) > 256 {
		return invalid("Composition requires name and type id or label (names/labels up to 256 characters)")
	}
	for _, v := range c.Placements {
		if !keys[v.Key] || v.Role == "" {
			return invalid("Placements require an existing source key and nonempty role")
		}
	}
	return nil
}
func (s *Service) prepare(ctx context.Context, p string, in Request, j *journal) error {
	if e := validate(in); e != nil {
		return e
	}
	pl := &plan{Name: in.Output.Composition.Name, Placements: in.Output.Composition.Placements}
	snapshots := map[string]store.Asset{}
	// No file I/O inside the SQL transaction. Native handles enforce confinement.
	for _, src := range in.Sources {
		if src.TaskOutputKey != "" {
			return missing("taskOutputKey not found; task output storage is introduced in M4")
		}
		if _, ok := snapshots[src.Path]; !ok {
			if e := s.checkSourcePending(ctx, p, in.RequestID, src.Path); e != nil {
				return e
			}
			if s.fault != nil {
				if e := s.fault("before_inspect"); e != nil {
					return e
				}
			}
			a, e := s.workspace.InspectDelivery(p, src.Path, uuid.NewString())
			if e != nil {
				// A file intent may start after the early check and temporarily
				// remove or replace the source. That gap is retryable, not blocked.
				if pending := s.checkSourcePending(ctx, p, in.RequestID, src.Path); pending != nil {
					return pending
				}
				return e
			}
			snapshots[src.Path] = a
		}
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var n int
	if e = tx.QueryRowContext(ctx, "SELECT count(*) FROM projects WHERE id=?", p).Scan(&n); e != nil {
		return e
	}
	if n == 0 {
		return missing("Project not found")
	}
	typ := in.Output.Composition.Type
	if typ.ID != "" {
		e = tx.QueryRowContext(ctx, "SELECT id,label FROM composition_types WHERE id=? AND project_id=?", typ.ID, p).Scan(&pl.TypeID, &pl.TypeLabel)
		if errors.Is(e, sql.ErrNoRows) {
			return missing("Composition type not found")
		}
		if e != nil {
			return e
		}
		if typ.Label != "" && typ.Label != pl.TypeLabel {
			return invalid("Composition type id and label disagree")
		}
	} else {
		e = tx.QueryRowContext(ctx, "SELECT id,label FROM composition_types WHERE project_id=? AND label=?", p, typ.Label).Scan(&pl.TypeID, &pl.TypeLabel)
		if errors.Is(e, sql.ErrNoRows) {
			pl.TypeID = uuid.NewString()
			pl.TypeLabel = typ.Label
			pl.NewType = true
		} else if e != nil {
			return e
		}
	}
	rows, e := tx.QueryContext(ctx, "SELECT id FROM compositions WHERE project_id=? AND type_id=? AND name=?", p, pl.TypeID, pl.Name)
	if e != nil {
		return e
	}
	for rows.Next() {
		if pl.CompositionID != "" {
			rows.Close()
			return invalid("Ambiguous composition target")
		}
		if e = rows.Scan(&pl.CompositionID); e != nil {
			rows.Close()
			return e
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	if pl.CompositionID == "" {
		pl.CompositionID = uuid.NewString()
		pl.NewComposition = true
	}
	allocated := map[string]plannedSource{}
	for _, src := range in.Sources {
		ps, ok := allocated[src.Path]
		if !ok {
			// Successful inspection can observe installed replacement bytes before
			// their file journal commits. Check reservations before rejecting the
			// still-old registered identity in this same admission transaction.
			if e = store.CheckFilePending(ctx, tx, p, src.Path); e != nil {
				return e
			}
			if e = store.CheckDeliveryPaths(ctx, tx, p, in.RequestID, src.Path); e != nil {
				return e
			}
			ps = plannedSource{Asset: snapshots[src.Path]}
			a := &ps.Asset
			var existing, status string
			var dev, inode sql.NullInt64
			var digest sql.NullString
			e = tx.QueryRowContext(ctx, "SELECT a.id,a.status,a.dev_id,a.inode,a.full_hash FROM assets a JOIN asset_projects ap ON ap.asset_id=a.id WHERE a.path=? AND ap.project_id=?", src.Path, p).Scan(&existing, &status, &dev, &inode, &digest)
			if e == nil {
				if !digest.Valid {
					e = tx.QueryRowContext(ctx, `SELECT json_extract(m.value,'$.digest') FROM file_operations f,json_each(f.source) m WHERE f.project_id=? AND f.stage='completed' AND json_extract(m.value,'$.asset.id')=? AND json_extract(m.value,'$.digest') IS NOT NULL LIMIT 1`, p, existing).Scan(&digest)
					if e != nil && !errors.Is(e, sql.ErrNoRows) {
						return e
					}
				}
				if status != "active" || !dev.Valid || !inode.Valid || dev.Int64 != *a.DevID || inode.Int64 != *a.Inode || !digest.Valid || digest.String != *a.FullHash {
					return invalid("Registered delivery source changed")
				}
				a.ID = existing
				ps.Existing = true
			} else if !errors.Is(e, sql.ErrNoRows) {
				return e
			} else {
				a.Path = "projects/" + p + "/files/" + a.ID + path.Ext(src.Path)
				a.Name = path.Base(a.Path)
			}
			if e = store.CheckFilePending(ctx, tx, p, src.Path, a.Path); e != nil {
				return e
			}
			if e = store.CheckDeliveryPaths(ctx, tx, p, in.RequestID, src.Path, a.Path); e != nil {
				return e
			}
			allocated[src.Path] = ps
		}
		ps.Source = src
		ps.VersionID = uuid.NewString()
		ps.ResourceID = uuid.NewString()
		for _, r := range in.Output.Resources {
			if r.Key != src.Key {
				continue
			}
			ps.Name = r.Name
			ps.BaseVersionID = r.BaseVersionID
			if r.Mode == "new_version" {
				var cid string
				e = tx.QueryRowContext(ctx, `SELECT r.id,r.composition_id FROM versions v JOIN resources r ON r.id=v.resource_id JOIN compositions c ON c.id=r.composition_id WHERE v.id=? AND c.project_id=?`, r.BaseVersionID, p).Scan(&ps.ResourceID, &cid)
				if errors.Is(e, sql.ErrNoRows) {
					return missing("Base version not found in project")
				}
				if e != nil {
					return e
				}
				if cid != pl.CompositionID {
					return invalid("new_version composition target is incompatible with base version")
				}
			}
		}
		pl.Sources = append(pl.Sources, ps)
	}
	raw, e := json.Marshal(pl)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO deliveries(project_id,request_id,input_json,stage,status,origin,plan_json) VALUES(?,?,?,'prepared','unknown',?,?) ON CONFLICT(project_id,request_id) DO UPDATE SET stage='prepared',plan_json=excluded.plan_json`, p, in.RequestID, j.Input, j.Origin, string(raw))
	if e != nil {
		return e
	}
	for _, ps := range pl.Sources {
		raw, e = json.Marshal(ps)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO delivery_sources(project_id,request_id,source_key,source_path,archive_path,asset_id,resource_id,version_id,base_version_id,stage,source_json) VALUES(?,?,?,?,?,?,?,?,?,'prepared',?)`, p, in.RequestID, ps.Source.Key, ps.Source.Path, ps.Asset.Path, ps.Asset.ID, ps.ResourceID, ps.VersionID, nullable(ps.BaseVersionID), string(raw))
		if e != nil {
			return e
		}
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	j.Plan = pl
	return nil
}

func (s *Service) checkSourcePending(ctx context.Context, p, key, source string) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = store.CheckFilePending(ctx, tx, p, source); e != nil {
		return e
	}
	return store.CheckDeliveryPaths(ctx, tx, p, key, source)
}
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
func (s *Service) commit(ctx context.Context, p, key string, j *journal) (Receipt, error) {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return Receipt{}, e
	}
	defer tx.Rollback()
	pl := j.Plan
	var n int
	if pl.NewType {
		if e = tx.QueryRowContext(ctx, "SELECT count(*) FROM composition_types WHERE project_id=? AND label=?", p, pl.TypeLabel).Scan(&n); e != nil {
			return Receipt{}, e
		}
		if n != 0 {
			return Receipt{}, invalid("Composition type target changed after preparation; use a new requestId")
		}
	} else {
		if e = tx.QueryRowContext(ctx, "SELECT count(*) FROM composition_types WHERE id=? AND project_id=? AND label=?", pl.TypeID, p, pl.TypeLabel).Scan(&n); e != nil {
			return Receipt{}, e
		}
		if n != 1 {
			return Receipt{}, invalid("Composition type target changed after preparation")
		}
	}
	if e = tx.QueryRowContext(ctx, "SELECT count(*) FROM compositions WHERE project_id=? AND type_id=? AND name=?", p, pl.TypeID, pl.Name).Scan(&n); e != nil {
		return Receipt{}, e
	}
	if (pl.NewComposition && n != 0) || (!pl.NewComposition && n != 1) {
		return Receipt{}, invalid("Composition target changed after preparation")
	}
	if !pl.NewComposition {
		if e = tx.QueryRowContext(ctx, "SELECT count(*) FROM compositions WHERE id=? AND project_id=? AND type_id=? AND name=?", pl.CompositionID, p, pl.TypeID, pl.Name).Scan(&n); e != nil {
			return Receipt{}, e
		}
		if n != 1 {
			return Receipt{}, invalid("Composition target changed after preparation")
		}
	}
	if pl.NewType {
		_, e = tx.ExecContext(ctx, "INSERT INTO composition_types(id,project_id,label) VALUES(?,?,?)", pl.TypeID, p, pl.TypeLabel)
		if e != nil {
			return Receipt{}, e
		}
	}
	if pl.NewComposition {
		_, e = tx.ExecContext(ctx, "INSERT INTO compositions(id,project_id,type_id,name) VALUES(?,?,?,?)", pl.CompositionID, p, pl.TypeID, pl.Name)
		if e != nil {
			return Receipt{}, e
		}
	}
	r := result("completed", nil)
	inserted := map[string]bool{}
	for _, ps := range pl.Sources {
		if e = store.CheckFilePending(ctx, tx, p, ps.Source.Path, ps.Asset.Path); e != nil {
			return Receipt{}, e
		}
		if !inserted[ps.Asset.ID] {
			if !ps.Existing {
				if e = workspace.InsertDeliveryAsset(ctx, tx, p, ps.Asset); e != nil {
					return Receipt{}, e
				}
			} else {
				if e = store.CheckAssetFilePending(ctx, tx, ps.Asset.ID); e != nil {
					return Receipt{}, e
				}
				var n int
				if e = tx.QueryRowContext(ctx, "SELECT count(*) FROM assets a JOIN asset_projects ap ON ap.asset_id=a.id WHERE a.id=? AND ap.project_id=? AND a.status='active' AND a.path=?", ps.Asset.ID, p, ps.Asset.Path).Scan(&n); e != nil {
					return Receipt{}, e
				}
				if n != 1 {
					return Receipt{}, invalid("Delivery asset changed")
				}
			}
			inserted[ps.Asset.ID] = true
		}
		if ps.BaseVersionID == "" {
			_, e = tx.ExecContext(ctx, "INSERT INTO resources(id,composition_id,description) VALUES(?,?,?)", ps.ResourceID, pl.CompositionID, ps.Name)
		} else {
			var n int
			e = tx.QueryRowContext(ctx, "SELECT count(*) FROM versions v JOIN resources r ON r.id=v.resource_id WHERE v.id=? AND r.id=? AND r.composition_id=?", ps.BaseVersionID, ps.ResourceID, pl.CompositionID).Scan(&n)
			if e == nil && n != 1 {
				e = missing("Base version no longer exists")
			}
		}
		if e != nil {
			return Receipt{}, e
		}
		_, e = tx.ExecContext(ctx, "INSERT INTO versions(id,resource_id,asset_id,source_version_id,origin,created_at) VALUES(?,?,?,?,?,?)", ps.VersionID, ps.ResourceID, ps.Asset.ID, nullable(ps.BaseVersionID), j.Origin, time.Now().UnixMilli())
		if e != nil {
			return Receipt{}, e
		}
		for _, v := range pl.Placements {
			if v.Key == ps.Source.Key {
				_, e = tx.ExecContext(ctx, `INSERT INTO composition_members(composition_id,resource_id,role) SELECT ?,?,? WHERE NOT EXISTS(SELECT 1 FROM composition_members WHERE composition_id=? AND resource_id=? AND role=?)`, pl.CompositionID, ps.ResourceID, v.Role, pl.CompositionID, ps.ResourceID, v.Role)
				if e != nil {
					return Receipt{}, e
				}
			}
		}
		r.Delivery.Versions = append(r.Delivery.Versions, Version{Key: ps.Source.Key, ResourceID: ps.ResourceID, VersionID: ps.VersionID, AssetID: ps.Asset.ID})
	}
	raw, _ := json.Marshal(r)
	_, e = tx.ExecContext(ctx, "UPDATE deliveries SET stage='completed',status='completed',receipt=? WHERE project_id=? AND request_id=?", string(raw), p, key)
	if e != nil {
		return Receipt{}, e
	}
	_, e = tx.ExecContext(ctx, "UPDATE delivery_sources SET stage='completed' WHERE project_id=? AND request_id=?", p, key)
	if e != nil {
		return Receipt{}, e
	}
	e = tx.Commit()
	return r, e
}
