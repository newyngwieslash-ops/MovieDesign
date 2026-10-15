package workspace

import (
	"context"

	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"github.com/zeebo/xxh3"
)

type Service struct {
	DB        *sql.DB
	Workspace *Workspace
	MaxUpload int64
	// ponytail: one service lock serializes local file intents; use per-project
	// locks only after measuring contention, with journal locking across services.
	mu sync.Mutex
}

func NewService(db *sql.DB, w *Workspace) *Service {
	return &Service{DB: db, Workspace: w, MaxUpload: 256 << 20}
}
func invalid(s string) error {
	return &store.DomainError{Status: 400, Code: "invalid_input", Message: s}
}
func missing(s string) error { return &store.DomainError{Status: 404, Code: "not_found", Message: s} }
func (s *Service) scope(ctx context.Context, p, key string) error {
	if e := (store.Scope{ProjectID: p, RequestID: key, Operation: "files"}).Validate(); e != nil || p == "_app" {
		return invalid("Invalid project_id or requestId")
	}
	var exists int
	if e := s.DB.QueryRowContext(ctx, "SELECT count(*) FROM projects WHERE id=?", p).Scan(&exists); e != nil {
		return e
	}
	if exists == 0 {
		return missing("Project not found")
	}
	return nil
}

type Input struct {
	RequestID string   `json:"requestId" validate:"required,max=128"`
	Path      string   `json:"path,omitempty"`
	Paths     []string `json:"paths,omitempty"`
	NewName   string   `json:"new_name,omitempty"`
	Target    string   `json:"target,omitempty"`
	Content   string   `json:"content,omitempty"`
	Unique    *bool    `json:"unique,omitempty"`
}
type move struct {
	Source         string       `json:"source"`
	Target         string       `json:"target"`
	Dev            int64        `json:"dev"`
	Inode          int64        `json:"inode"`
	Digest         string       `json:"digest,omitempty"`
	Asset          *store.Asset `json:"asset,omitempty"`
	Directory      bool         `json:"directory,omitempty"`
	Temporary      string       `json:"temporary,omitempty"`
	TemporaryDev   int64        `json:"temporary_dev,omitempty"`
	TemporaryInode int64        `json:"temporary_inode,omitempty"`
}
type journal struct {
	Operation, Input, Stage string
	Plan                    []move
	Receipt                 json.RawMessage
}

func (s *Service) load(ctx context.Context, p, key, op string, input any) (*journal, error) {
	if e := s.scope(ctx, p, key); e != nil {
		return nil, e
	}
	b, e := json.Marshal(input)
	if e != nil {
		return nil, e
	}
	b, e = store.CanonicalJSON(b)
	if e != nil {
		return nil, e
	}
	j := &journal{Operation: op, Input: string(b)}
	var source sql.NullString
	var receipt sql.NullString
	e = s.DB.QueryRowContext(ctx, "SELECT operation,input_json,stage,source,receipt FROM file_operations WHERE project_id=? AND request_id=?", p, key).Scan(&j.Operation, &j.Input, &j.Stage, &source, &receipt)
	if errors.Is(e, sql.ErrNoRows) {
		return &journal{Operation: op, Input: string(b)}, nil
	}
	if e != nil {
		return nil, e
	}
	if j.Operation != op || j.Input != string(b) {
		return nil, store.ErrRequestConflict
	}
	if source.Valid {
		if e = json.Unmarshal([]byte(source.String), &j.Plan); e != nil {
			return nil, e
		}
	}
	j.Receipt = json.RawMessage(receipt.String)
	return j, nil
}
func (s *Service) save(ctx context.Context, p, key string, j *journal) error {
	b, e := json.Marshal(j.Plan)
	if e != nil {
		return e
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var exists int
	if e = tx.QueryRowContext(ctx, "SELECT count(*) FROM projects WHERE id=?", p).Scan(&exists); e != nil {
		return e
	}
	if exists == 0 {
		return missing("Project not found")
	}
	if j.Stage == "prepared" {
		paths := make([]string, 0, len(j.Plan)*2)
		for _, m := range j.Plan {
			paths = append(paths, m.Source, m.Target)
		}
		rows, err := tx.QueryContext(ctx, pendingFilesQuery, p, key)
		if err != nil {
			return err
		}
		if e = checkPendingFiles(rows, paths...); e != nil {
			return e
		}
		if e = store.CheckDeliveryPaths(ctx, tx, p, "", paths...); e != nil {
			return e
		}
	}
	if j.Stage == "prepared" && (j.Operation == "delete" || j.Operation == "content") {
		for _, m := range j.Plan {
			if e = store.CheckTaskAssetPath(ctx, tx, p, m.Source); e != nil {
				return e
			}
			if !strings.HasPrefix(m.Source, prefix(p)+"files/") {
				continue
			}
			var n int
			e = tx.QueryRowContext(ctx, `SELECT count(*) FROM versions v JOIN assets a ON a.id=v.asset_id WHERE lower(a.path)=lower(?) OR substr(lower(a.path),1,length(?)+1)=lower(?)||'/'`, m.Source, m.Source, m.Source).Scan(&n)
			if e != nil {
				return e
			}
			if n != 0 {
				return invalid("Versioned files are immutable; use deliver/document_write")
			}
		}
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO file_operations(project_id,request_id,operation,input_json,stage,source,target,receipt) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(project_id,request_id) DO UPDATE SET stage=excluded.stage,source=excluded.source,target=excluded.target,receipt=excluded.receipt`, p, key, j.Operation, j.Input, j.Stage, string(b), targets(j.Plan), nullableJSON(j.Receipt))
	if e != nil {
		return e
	}
	return tx.Commit()
}
func targets(m []move) string {
	v := make([]string, 0, len(m))
	for _, x := range m {
		v = append(v, x.Target)
	}
	b, _ := json.Marshal(v)
	return string(b)
}
func nullableJSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}
func (s *Service) finish(ctx context.Context, p, key string, j *journal, receipt any, mutate func(*sql.Tx) error) (json.RawMessage, error) {
	b, e := json.Marshal(receipt)
	if e != nil {
		return nil, e
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	if mutate != nil {
		if e = mutate(tx); e != nil {
			return nil, e
		}
	}
	_, e = tx.ExecContext(ctx, "UPDATE file_operations SET stage='completed',receipt=? WHERE project_id=? AND request_id=?", string(b), p, key)
	if e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	j.Stage = "completed"
	j.Receipt = b
	return b, nil
}

const pendingFilesQuery = "SELECT request_id,source FROM file_operations WHERE project_id=? AND request_id<>? AND stage='prepared'"

func (s *Service) busy(ctx context.Context, p, key string, paths ...string) error {
	rows, e := s.DB.QueryContext(ctx, pendingFilesQuery, p, key)
	if e != nil {
		return e
	}
	return checkPendingFiles(rows, paths...)
}
func checkPendingFiles(rows *sql.Rows, paths ...string) error {
	defer rows.Close()
	for rows.Next() {
		var id string
		var raw sql.NullString
		if e := rows.Scan(&id, &raw); e != nil {
			return e
		}
		var moves []move
		if raw.Valid {
			if e := json.Unmarshal([]byte(raw.String), &moves); e != nil {
				return e
			}
		}
		for _, m := range moves {
			for _, v := range paths {
				if overlaps(v, m.Source) || overlaps(v, m.Target) {
					return store.ErrRequestPending
				}
			}
		}
	}
	return rows.Err()
}
func overlaps(a, b string) bool {
	a = strings.ToLower(a)
	b = strings.ToLower(b)
	return a != "" && b != "" && (a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/"))
}
func digestReader(r io.Reader, limit int64) (string, error) {
	h := xxh3.New()
	n, e := io.Copy(h, io.LimitReader(r, limit+1))
	if e == nil && n > limit {
		e = invalid("Upload exceeds size limit")
	}
	return fmt.Sprintf("%016x", h.Sum64()), e
}
func (s *Service) Stage(ctx context.Context, p, key, name string, r io.Reader) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := validLeaf(name); e != nil {
		return "", e
	}
	j, e := s.load(ctx, p, key, "upload", map[string]string{"name": name})
	if e != nil {
		return "", e
	}
	if j.Stage == "completed" {
		hash, e := digestReader(r, s.MaxUpload)
		if e != nil {
			return "", e
		}
		if hash != j.Plan[0].Digest {
			return "", store.ErrRequestConflict
		}
		var out struct {
			Path string `json:"path"`
		}
		e = json.Unmarshal(j.Receipt, &out)
		return out.Path, e
	}
	if j.Stage == "uploaded" {
		hash, e := digestReader(r, s.MaxUpload)
		if e != nil {
			return "", e
		}
		if hash != j.Plan[0].Digest {
			return "", store.ErrRequestConflict
		}
		stage := j.Plan[0].Target
		_, e = s.finish(ctx, p, key, j, map[string]string{"path": stage}, nil)
		return stage, e
	}
	if j.Stage == "uploading" && len(j.Plan) > 0 && j.Plan[0].Inode != 0 {
		m := j.Plan[0]
		f, e := s.Workspace.open(p, m.Target, true)
		if e == nil {
			d, i, e := fileIdentity(f)
			if e != nil || d != m.Dev || i != m.Inode {
				f.Close()
				return "", invalid("Partial upload identity changed")
			}
			e = nativeRemove(f)
			f.Close()
			if e != nil {
				return "", e
			}
		} else if !errors.Is(e, os.ErrNotExist) {
			return "", e
		}
	}
	stage := prefix(p) + "staging/" + uuid.NewString() + "/" + name
	j.Plan = []move{{Target: stage}}
	j.Stage = "uploading"
	if e = s.save(ctx, p, key, j); e != nil {
		return "", e
	}
	if e = s.Workspace.mkdir(p, path.Dir(stage)); e != nil {
		return "", e
	}
	f, e := s.Workspace.create(p, stage)
	if e != nil {
		return "", e
	}
	j.Plan[0].Dev, j.Plan[0].Inode, e = fileIdentity(f)
	if e != nil {
		f.Close()
		return "", e
	}
	if e = s.save(ctx, p, key, j); e != nil {
		f.Close()
		return "", e
	}
	h := xxh3.New()
	e = writeClosed(f, io.TeeReader(&contextReader{ctx: ctx, r: r}, h), s.MaxUpload)
	if e != nil {
		return "", e
	}
	j.Plan[0].Digest = fmt.Sprintf("%016x", h.Sum64())
	f, e = s.Workspace.OpenRegular(p, stage)
	if e != nil {
		return "", e
	}
	j.Plan[0].Dev, j.Plan[0].Inode, e = fileIdentity(f)
	f.Close()
	if e != nil {
		return "", e
	}
	j.Stage = "uploaded"
	if e = s.save(ctx, p, key, j); e != nil {
		return "", e
	}
	_, e = s.finish(ctx, p, key, j, map[string]string{"path": stage}, nil)
	return stage, e
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *contextReader) Read(b []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.r.Read(b)
}
func (s *Service) Commit(ctx context.Context, p, key, stage string) (store.Asset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var zero store.Asset
	j, e := s.load(ctx, p, key, "commit", map[string]string{"path": stage})
	if e != nil {
		return zero, e
	}
	if j.Stage == "completed" {
		e = json.Unmarshal(j.Receipt, &zero)
		return zero, e
	}
	if _, e = relative(p, stage); e != nil {
		return zero, e
	}
	if !strings.HasPrefix(stage, prefix(p)+"staging/") {
		return zero, invalid("Expected a staged upload")
	}
	if j.Stage == "" {
		var count int
		e = s.DB.QueryRowContext(ctx, "SELECT count(*) FROM file_operations WHERE project_id=? AND operation='upload' AND stage='completed' AND json_extract(receipt,'$.path')=?", p, stage).Scan(&count)
		if e != nil {
			return zero, e
		}
		if count != 1 {
			return zero, invalid("Upload is not complete")
		}
		var uploadPlan string
		if e = s.DB.QueryRowContext(ctx, "SELECT source FROM file_operations WHERE project_id=? AND operation='upload' AND stage='completed' AND json_extract(receipt,'$.path')=?", p, stage).Scan(&uploadPlan); e != nil {
			return zero, e
		}
		var original []move
		if e = json.Unmarshal([]byte(uploadPlan), &original); e != nil || len(original) != 1 {
			return zero, invalid("Invalid upload journal")
		}
		if e = s.busy(ctx, p, key, stage); e != nil {
			return zero, e
		}
		f, e := s.Workspace.OpenRegular(p, stage)
		if e != nil {
			return zero, e
		}
		fp, e := Fingerprint(f, false)
		digest, hashErr := digestReader(io.NewSectionReader(f, 0, 1<<63-1), s.MaxUpload)
		f.Close()
		if hashErr != nil {
			return zero, hashErr
		}
		if fp.DevID != original[0].Dev || fp.Inode != original[0].Inode || digest != original[0].Digest {
			return zero, invalid("Staged upload changed")
		}
		if e != nil {
			return zero, e
		}
		id := uuid.NewString()
		a := asset(id, prefix(p)+"files/"+id+"/"+path.Base(stage), fp)
		j.Plan = []move{{Source: stage, Target: a.Path, Dev: fp.DevID, Inode: fp.Inode, Digest: digest, Asset: &a}}
		j.Stage = "prepared"
		if e = s.save(ctx, p, key, j); e != nil {
			return zero, e
		}
	}
	m := j.Plan[0]
	if e = s.Workspace.mkdir(p, path.Dir(m.Target)); e != nil {
		return zero, e
	}
	if e = s.applyMove(p, m); e != nil {
		return zero, e
	}
	a := *m.Asset
	_, e = s.finish(ctx, p, key, j, a, func(tx *sql.Tx) error { return insertAsset(ctx, tx, p, a) })
	return a, e
}
func asset(id, v string, f FileFingerprint) store.Asset {
	now := time.Now().UnixMilli()
	return store.Asset{ID: id, Path: v, Status: "active", Size: f.Size, MtimeMS: f.MtimeMS, DevID: &f.DevID, Inode: &f.Inode, QuickHash: &f.QuickHash, Width: f.Width, Height: f.Height, MIMEType: &f.MIME, Name: path.Base(v), CreatedAt: now, UpdatedAt: now}
}
func insertAsset(ctx context.Context, tx *sql.Tx, p string, a store.Asset) error {
	_, e := tx.ExecContext(ctx, `INSERT INTO assets(id,path,status,size,mtime_ms,dev_id,inode,quick_hash,full_hash,width,height,mime_type,name,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, a.ID, a.Path, a.Status, a.Size, a.MtimeMS, a.DevID, a.Inode, a.QuickHash, a.FullHash, a.Width, a.Height, a.MIMEType, a.Name, a.CreatedAt, a.UpdatedAt)
	if e == nil {
		_, e = tx.ExecContext(ctx, "INSERT INTO asset_projects(asset_id,project_id) VALUES(?,?)", a.ID, p)
	}
	return e
}
func (s *Service) applyMove(p string, m move) error {
	rel, e := relative(p, m.Source)
	if e != nil {
		return e
	}
	_, release, e := s.Workspace.heldParent(p, path.Dir(rel))
	if e != nil {
		return e
	}
	defer release()
	// A completed move is identified by the held target, even if a later step
	// already installed a new identity at the original source (content writes).
	if target, e := s.Workspace.open(p, m.Target, false); e == nil {
		d, i, err := fileIdentity(target)
		if err == nil && d == m.Dev && i == m.Inode {
			err = verifyDigest(target, m.Digest)
			target.Close()
			return err
		}
		target.Close()
	}
	f, e := s.Workspace.open(p, m.Source, false)
	if errors.Is(e, os.ErrNotExist) {
		f, e = s.Workspace.open(p, m.Target, false)
		if e != nil {
			return e
		}
		defer f.Close()
		d, i, e := fileIdentity(f)
		if e != nil {
			return e
		}
		if d != m.Dev || i != m.Inode {
			return invalid("Recovery target identity changed")
		}
		return verifyDigest(f, m.Digest)
	}
	if e != nil {
		return e
	}
	defer f.Close()
	d, i, e := fileIdentity(f)
	if e != nil {
		return e
	}
	if d != m.Dev || i != m.Inode {
		return invalid("Source identity changed")
	}
	if e = verifyDigest(f, m.Digest); e != nil {
		return e
	}
	return s.Workspace.rename(p, m.Source, m.Target, f)
}
func verifyDigest(f *os.File, digest string) error {
	if digest == "" {
		return nil
	}
	actual, e := digestReader(io.NewSectionReader(f, 0, 1<<63-1), 1<<62)
	if e != nil {
		return e
	}
	if actual != digest {
		return invalid("File content changed during recovery")
	}
	return nil
}
func (s *Service) referenced(ctx context.Context, p, v string) error {
	var n int
	e := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM versions v JOIN assets a ON a.id=v.asset_id JOIN asset_projects ap ON ap.asset_id=a.id WHERE ap.project_id=? AND (lower(a.path)=lower(?) OR substr(lower(a.path),1,length(?)+1)=lower(?)||'/')`, p, v, v, v).Scan(&n)
	if e != nil {
		return e
	}
	if n != 0 {
		return invalid("Versioned files are immutable; create a new Version through deliver/document_write")
	}
	return nil
}
func (s *Service) managed(p, v string) error {
	if _, e := relative(p, v); e != nil {
		return e
	}
	if !strings.HasPrefix(v, prefix(p)+"files/") {
		return invalid("Expected managed files path")
	}
	return nil
}
func (s *Service) Mutate(ctx context.Context, p, op string, in Input) (json.RawMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, e := s.load(ctx, p, in.RequestID, op, in)
	if e != nil {
		return nil, e
	}
	if j.Stage == "completed" {
		return j.Receipt, nil
	}
	switch op {
	case "mkdir", "rename", "copy", "delete", "content":
	default:
		return nil, invalid("Unknown file operation")
	}
	if j.Stage == "" {
		paths := in.Paths
		if op != "copy" && op != "delete" {
			paths = []string{in.Path}
		}
		if len(paths) == 0 || len(paths) > 100 {
			return nil, invalid("paths must contain 1–100 entries")
		}
		for i, v := range paths {
			if e = s.managed(p, v); e != nil {
				return nil, e
			}
			for _, other := range paths[:i] {
				if overlaps(v, other) {
					return nil, invalid("Overlapping paths")
				}
			}
		}
		if e = s.busy(ctx, p, in.RequestID, append(paths, in.Target)...); e != nil {
			return nil, e
		}
		switch op {
		case "mkdir":
			if e = validPath(in.Path); e != nil {
				return nil, e
			}
			j.Plan = []move{{Target: in.Path, Directory: true}}
		case "rename":
			if e = validLeaf(in.NewName); e != nil {
				return nil, e
			}
			m, e := s.planMove(p, in.Path, path.Join(path.Dir(in.Path), in.NewName))
			if e != nil {
				return nil, e
			}
			j.Plan = []move{m}
		case "delete":
			for _, v := range paths {
				if e = s.referenced(ctx, p, v); e != nil {
					return nil, e
				}
				m, e := s.planMove(p, v, prefix(p)+"recycle/"+uuid.NewString()+"/"+path.Base(v))
				if e != nil {
					return nil, e
				}
				j.Plan = append(j.Plan, m)
			}
		case "copy":
			if e = s.managed(p, in.Target+"/placeholder"); e != nil {
				return nil, e
			}
			dir, e := s.Workspace.open(p, in.Target, false)
			if e != nil {
				return nil, e
			}
			st, e := dir.Stat()
			dir.Close()
			if e != nil || !st.IsDir() {
				return nil, invalid("Copy target must be an existing directory")
			}
			for _, v := range paths {
				f, e := s.Workspace.OpenRegular(p, v)
				if e != nil {
					return nil, e
				}
				fp, e := Fingerprint(f, false)
				f.Close()
				if e != nil {
					return nil, e
				}
				target := path.Join(in.Target, path.Base(v))
				for _, m := range j.Plan {
					if strings.EqualFold(m.Target, target) {
						return nil, invalid("Copy targets collide")
					}
				}
				if f, e := s.Workspace.open(p, target, false); e == nil {
					f.Close()
					return nil, &os.PathError{Op: "copy", Path: target, Err: os.ErrExist}
				} else if !errors.Is(e, os.ErrNotExist) {
					return nil, e
				}
				id := uuid.NewString()
				a := asset(id, target, fp)
				j.Plan = append(j.Plan, move{Source: v, Target: target, Dev: fp.DevID, Inode: fp.Inode, Asset: &a})
			}
		case "content":
			if e = s.referenced(ctx, p, in.Path); e != nil {
				return nil, e
			}
			ext := strings.ToLower(path.Ext(in.Path))
			if ext != ".txt" && ext != ".md" && ext != ".json" && ext != ".csv" && ext != ".yaml" && ext != ".yml" {
				return nil, invalid("Content writes support document files only")
			}
			if len(in.Content) > 1<<20 {
				return nil, invalid("Content exceeds 1 MiB")
			}
			f, err := s.Workspace.OpenRegular(p, in.Path)
			if err == nil {
				if in.Unique != nil && *in.Unique {
					f.Close()
					return nil, invalid("File already exists")
				}
				d, i, e := fileIdentity(f)
				f.Close()
				if e != nil {
					return nil, e
				}
				j.Plan = append(j.Plan, move{Source: in.Path, Target: prefix(p) + "recycle/" + uuid.NewString() + "/" + path.Base(in.Path), Dev: d, Inode: i})
			} else if !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
			id := uuid.NewString()
			tmp := prefix(p) + "staging/" + id + "/" + path.Base(in.Path)
			j.Plan = append(j.Plan, move{Source: tmp, Target: in.Path, Asset: &store.Asset{ID: id}})
		}
		j.Stage = "prepared"
	}
	// Publish the complete plan and revalidate reservations on recovery before
	// any filesystem change. Suffix candidates pass through save as well.
	if e = s.save(ctx, p, in.RequestID, j); e != nil {
		return nil, e
	}
	switch op {
	case "mkdir":
		e = s.Workspace.mkdir(p, j.Plan[0].Target)
	case "copy":
		e = s.applyCopies(ctx, p, in.RequestID, j)
	case "content":
		e = s.applyContent(ctx, p, in.RequestID, j, in.Content)
	default:
		for n := range j.Plan {
			m := &j.Plan[n]
			if op == "delete" {
				if e = s.Workspace.mkdir(p, path.Dir(m.Target)); e != nil {
					break
				}
			}
			e = s.applyMove(p, *m)
			if op == "rename" && errors.Is(e, os.ErrExist) {
				for suffix := 1; suffix <= 1000; suffix++ {
					m.Target = path.Join(path.Dir(in.Path), collisionName(in.NewName, suffix))
					if e = s.save(ctx, p, in.RequestID, j); e != nil {
						break
					}
					e = s.applyMove(p, *m)
					if !errors.Is(e, os.ErrExist) {
						break
					}
				}
			}
			if e != nil {
				break
			}
		}
	}
	if e != nil {
		return nil, e
	}
	out := map[string]any{"ok": true}
	if op == "rename" {
		out["new_path"] = j.Plan[0].Target
		out["new_name"] = path.Base(j.Plan[0].Target)
	}
	return s.finish(ctx, p, in.RequestID, j, out, func(tx *sql.Tx) error { return s.finalize(ctx, tx, p, op, j) })
}
func (s *Service) planMove(p, src, dst string) (move, error) {
	m := move{Source: src, Target: dst}
	f, e := s.Workspace.open(p, src, false)
	if e != nil {
		return m, e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return m, e
	}
	m.Directory = st.IsDir()
	m.Dev, m.Inode, e = fileIdentity(f)
	return m, e
}

// Private copies are complete before any published path changes. A recovered
// partial temporary file is removed only through its recorded identity handle.
func (s *Service) prepareBytes(ctx context.Context, p, key string, j *journal, m *move, r io.Reader, limit int64) error {
	if m.Digest != "" {
		return nil
	}
	if m.Temporary != "" {
		f, e := s.Workspace.open(p, m.Temporary, true)
		if e == nil {
			d, i, err := fileIdentity(f)
			if err != nil {
				f.Close()
				return err
			}
			if m.TemporaryInode != 0 && (d != m.TemporaryDev || i != m.TemporaryInode) {
				f.Close()
				return invalid("Temporary identity changed")
			}
			if m.TemporaryInode != 0 {
				e = nativeRemove(f)
			}
			f.Close()
			if e != nil {
				return e
			}
		} else if !errors.Is(e, os.ErrNotExist) {
			return e
		}
	}
	m.Temporary = prefix(p) + "staging/" + uuid.NewString() + "/" + path.Base(m.Target)
	m.TemporaryDev = 0
	m.TemporaryInode = 0
	if e := s.save(ctx, p, key, j); e != nil {
		return e
	}
	if e := s.Workspace.mkdir(p, path.Dir(m.Temporary)); e != nil {
		return e
	}
	f, e := s.Workspace.create(p, m.Temporary)
	if e != nil {
		return e
	}
	m.TemporaryDev, m.TemporaryInode, e = fileIdentity(f)
	if e != nil {
		f.Close()
		return e
	}
	if e = s.save(ctx, p, key, j); e != nil {
		f.Close()
		return e
	}
	if e = writeClosed(f, r, limit); e != nil {
		return e
	}
	f, e = s.Workspace.open(p, m.Temporary, true)
	if e != nil {
		return e
	}
	fp, e := Fingerprint(f, true)
	f.Close()
	if e != nil {
		return e
	}
	a := asset(m.Asset.ID, m.Target, fp)
	m.Asset = &a
	m.Digest = fp.FullHash
	return s.save(ctx, p, key, j)
}
func (s *Service) publishBytes(p string, m move) error {
	return s.applyMove(p, move{Source: m.Temporary, Target: m.Target, Dev: m.TemporaryDev, Inode: m.TemporaryInode, Digest: m.Digest})
}
func (s *Service) applyCopies(ctx context.Context, p, key string, j *journal) error {
	for n := range j.Plan {
		m := &j.Plan[n]
		if m.Digest != "" {
			continue
		}
		src, e := s.Workspace.OpenRegular(p, m.Source)
		if e != nil {
			return e
		}
		d, i, e := fileIdentity(src)
		if e != nil || d != m.Dev || i != m.Inode {
			src.Close()
			return invalid("Copy source identity changed")
		}
		e = s.prepareBytes(ctx, p, key, j, m, src, s.MaxUpload)
		src.Close()
		if e != nil {
			return e
		}
	}
	for _, m := range j.Plan {
		if e := s.publishBytes(p, m); e != nil {
			return e
		}
	}
	return nil
}
func (s *Service) applyContent(ctx context.Context, p, key string, j *journal, content string) error {
	m := &j.Plan[len(j.Plan)-1]
	if e := s.prepareBytes(ctx, p, key, j, m, strings.NewReader(content), 1<<20); e != nil {
		return e
	}
	if len(j.Plan) == 2 {
		old := j.Plan[0]
		if e := s.Workspace.mkdir(p, path.Dir(old.Target)); e != nil {
			return e
		}
		if e := s.applyMove(p, old); e != nil {
			return e
		}
	}
	if e := s.Workspace.mkdir(p, path.Dir(m.Target)); e != nil {
		return e
	}
	return s.publishBytes(p, *m)
}
func (s *Service) finalize(ctx context.Context, tx *sql.Tx, p, op string, j *journal) error {
	for i, m := range j.Plan {
		switch op {
		case "rename", "delete":
			rows, e := tx.QueryContext(ctx, `SELECT a.id,a.path FROM assets a JOIN asset_projects ap ON ap.asset_id=a.id WHERE ap.project_id=? AND (lower(a.path)=lower(?) OR substr(lower(a.path),1,length(?)+1)=lower(?)||'/')`, p, m.Source, m.Source, m.Source)
			if e != nil {
				return e
			}
			type item struct{ id, v string }
			var all []item
			for rows.Next() {
				var x item
				if e = rows.Scan(&x.id, &x.v); e != nil {
					rows.Close()
					return e
				}
				all = append(all, x)
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return e
			}
			for _, x := range all {
				status := "active"
				if op == "delete" {
					status = "missing"
				}
				target := m.Target + x.v[len(m.Source):]
				_, e = tx.ExecContext(ctx, "UPDATE assets SET path=?,name=?,status=?,updated_at=? WHERE id=?", target, path.Base(target), status, time.Now().UnixMilli(), x.id)
				if e != nil {
					return e
				}
			}
		case "copy":
			if e := insertAsset(ctx, tx, p, *m.Asset); e != nil {
				return e
			}
		case "content":
			if i < len(j.Plan)-1 {
				_, e := tx.ExecContext(ctx, "UPDATE assets SET path=?,status='missing',updated_at=? WHERE path=?", m.Target, time.Now().UnixMilli(), m.Source)
				if e != nil {
					return e
				}
			} else if e := insertAsset(ctx, tx, p, *m.Asset); e != nil {
				return e
			}
		}
	}
	return nil
}

type Entry struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Type   string `json:"type"`
	Folder string `json:"folder"`
	Size   int64  `json:"size"`
	URL    string `json:"url"`
	Mtime  int64  `json:"mtime"`
}
type Listing struct {
	Files    []Entry        `json:"files"`
	Folders  []string       `json:"folders"`
	Coverage store.Coverage `json:"coverage"`
}

func (s *Service) List(ctx context.Context, p, kind, order string, offset, limit int) (Listing, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := Listing{Files: []Entry{}, Folders: []string{}}
	if e := s.scope(ctx, p, "query"); e != nil {
		return out, e
	}
	if offset < 0 || limit < 1 || limit > 200 {
		return out, invalid("Invalid pagination")
	}
	if order != "" && order != "name" && order != "time" {
		return out, invalid("Invalid sort")
	}
	if kind != "" && kind != "all" && kind != "image" && kind != "video" && kind != "audio" && kind != "document" {
		return out, invalid("Invalid type")
	}
	var files []Entry
	var visit func(string) error
	visit = func(v string) error {
		f, e := s.Workspace.openAccess(p, v, false, false)
		if e != nil {
			return e
		}
		entries, e := f.ReadDir(-1)
		closeErr := f.Close()
		if e == nil {
			e = closeErr
		}
		if e != nil {
			return e
		}
		for _, entry := range entries {
			name := entry.Name()
			if e = validLeaf(name); e != nil {
				return e
			}
			child := v + "/" + name
			cf, e := s.Workspace.openAccess(p, child, false, false)
			if e != nil {
				return e
			}
			st, e := cf.Stat()
			if e != nil {
				cf.Close()
				return e
			}
			if st.IsDir() {
				cf.Close()
				out.Folders = append(out.Folders, child)
				if e = visit(child); e != nil {
					return e
				}
				continue
			}
			fp, e := Fingerprint(cf, false)
			cf.Close()
			if e != nil {
				return e
			}
			typ := strings.Split(fp.MIME, "/")[0]
			if typ != "image" && typ != "video" && typ != "audio" {
				typ = "document"
			}
			if kind == "" || kind == "all" || kind == typ {
				files = append(files, Entry{Name: name, Path: child, Type: typ, Folder: v, Size: fp.Size, Mtime: fp.MtimeMS})
			}
		}
		return nil
	}
	root := prefix(p) + "files"
	e := visit(root)
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return out, e
	}
	sort.Slice(files, func(i, j int) bool {
		if order == "name" {
			return files[i].Name < files[j].Name
		}
		if files[i].Mtime == files[j].Mtime {
			return files[i].Path < files[j].Path
		}
		return files[i].Mtime > files[j].Mtime
	})
	n := len(files)
	start := min(offset, n)
	end := min(start+limit, n)
	out.Files = append(out.Files, files[start:end]...)
	sort.Strings(out.Folders)
	out.Coverage = store.Coverage{Total: n, NextOffset: end, Exhausted: end == n}
	return out, nil
}

func (s *Service) OpenContent(ctx context.Context, p, v string) (*os.File, error) {
	if e := s.scope(ctx, p, "read"); e != nil {
		return nil, e
	}
	if e := s.managed(p, v); e != nil {
		return nil, e
	}
	return s.Workspace.OpenRegular(p, v)
}

// Intake bounds multipart spooling inside the project even when file precedes
// requestId. It is private transport state, never a public upload receipt.
func (s *Service) Intake(ctx context.Context, p string, r io.Reader) (*os.File, func(), error) {
	key := "intake_" + uuid.NewString()
	v, e := s.Stage(ctx, p, key, "multipart.bin", r)
	cleanup := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		j, err := s.load(context.Background(), p, key, "upload", map[string]string{"name": "multipart.bin"})
		if err != nil || len(j.Plan) == 0 {
			return
		}
		m := j.Plan[0]
		f, err := s.Workspace.open(p, m.Target, true)
		if err == nil {
			d, i, err := fileIdentity(f)
			if err == nil && d == m.Dev && i == m.Inode {
				err = nativeRemove(f)
			}
			f.Close()
			if err != nil {
				return
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return
		}
		j.Stage = "retired"
		_ = s.save(context.Background(), p, key, j)
	}
	if e != nil {
		cleanup()
		return nil, nil, e
	}
	f, e := s.Workspace.OpenRegular(p, v)
	if e != nil {
		cleanup()
		return nil, nil, e
	}
	return f, func() { f.Close(); cleanup() }, nil
}

// Register validates a published file and records only facts obtained from its
// held handle. HTTP and later MCP callers share this boundary.
func (s *Service) Register(ctx context.Context, p, key, v string) (store.Asset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var zero store.Asset
	j, e := s.load(ctx, p, key, "register", map[string]string{"path": v})
	if e != nil {
		return zero, e
	}
	if j.Stage == "completed" {
		e = json.Unmarshal(j.Receipt, &zero)
		return zero, e
	}
	if e = s.managed(p, v); e != nil {
		return zero, e
	}
	if e = s.busy(ctx, p, key, v); e != nil {
		return zero, e
	}
	f, e := s.Workspace.OpenRegular(p, v)
	if e != nil {
		return zero, e
	}
	defer f.Close()
	fp, e := Fingerprint(f, true)
	if e != nil {
		return zero, e
	}
	var existingID string
	e = s.DB.QueryRowContext(ctx, "SELECT a.id FROM assets a JOIN asset_projects ap ON ap.asset_id=a.id WHERE a.path=? AND ap.project_id=?", v, p).Scan(&existingID)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return zero, e
	}
	a := asset(uuid.NewString(), v, fp)
	a.FullHash = &fp.FullHash
	if existingID != "" {
		raw, e := (store.Objects{DB: s.DB}).Get(ctx, p, "assets", existingID)
		if e != nil {
			return zero, e
		}
		if e = json.Unmarshal(raw, &a); e != nil {
			return zero, e
		}
		if a.DevID == nil || a.Inode == nil || *a.DevID != fp.DevID || *a.Inode != fp.Inode || a.Size != fp.Size || a.MtimeMS != fp.MtimeMS || a.QuickHash == nil || *a.QuickHash != fp.QuickHash {
			return zero, invalid("Registered file changed; import new bytes through upload or document_write")
		}
		var original string
		if a.FullHash != nil {
			original = *a.FullHash
		} else {
			_ = s.DB.QueryRowContext(ctx, `SELECT json_extract(m.value,'$.digest') FROM file_operations f,json_each(f.source) m WHERE f.project_id=? AND f.stage='completed' AND json_extract(m.value,'$.asset.id')=? AND json_extract(m.value,'$.digest') IS NOT NULL LIMIT 1`, p, a.ID).Scan(&original)
		}
		if original == "" || original != fp.FullHash {
			return zero, invalid("Registered file content changed")
		}
		a.FullHash = &fp.FullHash
	}
	if j.Stage == "prepared" {
		old := j.Plan[0]
		if old.Dev != fp.DevID || old.Inode != fp.Inode || old.Digest != fp.FullHash {
			return zero, invalid("Registration source changed")
		}
		a = *old.Asset
	} else {
		j.Plan = []move{{Source: v, Target: v, Dev: fp.DevID, Inode: fp.Inode, Digest: fp.FullHash, Asset: &a}}
		j.Stage = "prepared"
		if e = s.save(ctx, p, key, j); e != nil {
			return zero, e
		}
	}
	_, e = s.finish(ctx, p, key, j, a, func(tx *sql.Tx) error {
		if existingID != "" {
			_, err := tx.ExecContext(ctx, "UPDATE assets SET full_hash=? WHERE id=?", fp.FullHash, a.ID)
			return err
		}
		return insertAsset(ctx, tx, p, a)
	})
	return a, e
}
