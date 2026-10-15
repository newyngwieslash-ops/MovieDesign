package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
)

func setup(t *testing.T) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	w, e := Open(root)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { w.Close() })
	db, e := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	p, e := (store.Objects{DB: db}).CreateProject(context.Background(), "test", "project")
	if e != nil {
		t.Fatal(e)
	}
	return NewService(db, w), p.ID
}
func TestRejectEscape(t *testing.T) {
	s, p := setup(t)
	for _, v := range []string{"../secret", "C:/secret", "//server/share/file", "a/../../b", "projects/other/files/a", "projects/" + p + "/files/a:ads", "projects/" + p + "/files/CON", "projects/" + p + "/files/a."} {
		if f, e := s.Workspace.OpenRegular(p, v); e == nil {
			f.Close()
			t.Errorf("accepted %q", v)
		}
	}
}
func TestFingerprintAndFilesReplay(t *testing.T) {
	s, p := setup(t)
	ctx := context.Background()
	stage, e := s.Stage(ctx, p, "upload", "hello.txt", strings.NewReader("hello"))
	if e != nil {
		t.Fatal(e)
	}
	a, e := s.Commit(ctx, p, "commit", stage)
	if e != nil {
		t.Fatal(e)
	}
	b, e := s.Commit(ctx, p, "commit", stage)
	if e != nil || a.ID != b.ID {
		t.Fatalf("replay %v %+v", e, b)
	}
	if _, e = s.Stage(ctx, p, "upload", "hello.txt", strings.NewReader("other")); !errors.Is(e, store.ErrRequestConflict) {
		t.Fatalf("different upload %v", e)
	}
	f, e := s.Workspace.OpenRegular(p, a.Path)
	if e != nil {
		t.Fatal(e)
	}
	fp, e := Fingerprint(f, true)
	f.Close()
	if e != nil || fp.QuickHash == "" || fp.Inode == 0 || fp.FullHash == "" {
		t.Fatalf("fingerprint %+v %v", fp, e)
	}
	out, e := s.Mutate(ctx, p, "rename", Input{RequestID: "rename", Path: a.Path, NewName: "renamed.txt"})
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(out), "renamed.txt") {
		t.Fatal(string(out))
	}
	raw, e := (store.Objects{DB: s.DB}).Get(ctx, p, "assets", a.ID)
	if e != nil || !strings.Contains(string(raw), "renamed.txt") {
		t.Fatalf("stable asset %s %v", raw, e)
	}
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func TestFilesInterruptedAndLimit(t *testing.T) {
	s, p := setup(t)
	s.MaxUpload = 4
	if _, e := s.Stage(context.Background(), p, "large", "a.txt", strings.NewReader("12345")); e == nil {
		t.Fatal("accepted oversize")
	}
	if _, e := s.Stage(context.Background(), p, "broken", "a.txt", brokenReader{}); e == nil {
		t.Fatal("accepted disconnected")
	}
	if _, e := s.Stage(context.Background(), p, "broken", "a.txt", strings.NewReader("ok")); e != nil {
		t.Fatal(e)
	}
}
func TestFingerprintSameSizeTime(t *testing.T) {
	s, p := setup(t)
	a, e := s.Stage(context.Background(), p, "one", "a.txt", strings.NewReader("aaaa"))
	if e != nil {
		t.Fatal(e)
	}
	f, e := s.Workspace.OpenRegular(p, a)
	if e != nil {
		t.Fatal(e)
	}
	x, e := Fingerprint(f, false)
	f.Close()
	if e != nil {
		t.Fatal(e)
	}
	b, e := s.Stage(context.Background(), p, "two", "b.txt", strings.NewReader("bbbb"))
	if e != nil {
		t.Fatal(e)
	}
	f, e = s.Workspace.OpenRegular(p, b)
	if e != nil {
		t.Fatal(e)
	}
	y, e := Fingerprint(f, false)
	f.Close()
	if e != nil || x.QuickHash == y.QuickHash {
		t.Fatal("different bytes merged")
	}
}

func put(t *testing.T, s *Service, p, key, name, content string) store.Asset {
	t.Helper()
	v, e := s.Stage(context.Background(), p, key, name, strings.NewReader(content))
	if e != nil {
		t.Fatal(e)
	}
	a, e := s.Commit(context.Background(), p, key+"commit", v)
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func TestFilesDirectoryRenameCopyAndRecovery(t *testing.T) {
	s, p := setup(t)
	ctx := context.Background()
	a := put(t, s, p, "a", "a.txt", "first")
	dir := path.Dir(a.Path)
	out, e := s.Mutate(ctx, p, "rename", Input{RequestID: "dirrename", Path: dir, NewName: "renamed"})
	if e != nil {
		t.Fatal(e)
	}
	var renamed struct {
		Path string `json:"new_path"`
	}
	json.Unmarshal(out, &renamed)
	a.Path = renamed.Path + "/a.txt"
	f, e := s.Workspace.OpenRegular(p, a.Path)
	if e != nil {
		t.Fatal(e)
	}
	fp, e := Fingerprint(f, false)
	f.Close()
	if e != nil || *a.Inode != fp.Inode {
		t.Fatalf("rename identity %+v %v", fp, e)
	}
	raw, e := (store.Objects{DB: s.DB}).Get(ctx, p, "assets", a.ID)
	if e != nil || !strings.Contains(string(raw), a.Path) {
		t.Fatalf("descendant path %s %v", raw, e)
	}
	target := prefix(p) + "files/copies"
	if _, e = s.Mutate(ctx, p, "mkdir", Input{RequestID: "mkdir", Path: target}); e != nil {
		t.Fatal(e)
	}
	input := Input{RequestID: "copy", Paths: []string{a.Path}, Target: target}
	if _, e = s.Mutate(ctx, p, "copy", input); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Mutate(ctx, p, "copy", Input{RequestID: "collision", Paths: []string{a.Path}, Target: target}); !errors.Is(e, os.ErrExist) {
		t.Fatalf("copy collision %v", e)
	}
	// Simulate response loss after filesystem and SQL commit: a reopened service replays.
	s2 := NewService(s.DB, s.Workspace)
	if _, e = s2.Mutate(ctx, p, "copy", input); e != nil {
		t.Fatal(e)
	}
	content := Input{RequestID: "edit", Path: a.Path, Content: "updated"}
	if _, e = s.Mutate(ctx, p, "content", content); e != nil {
		t.Fatal(e)
	}
	// Roll back only SQL finalization to simulate process exit after publication.
	j, e := s.load(ctx, p, "edit", "content", content)
	if e != nil {
		t.Fatal(e)
	}
	replacement := j.Plan[len(j.Plan)-1].Asset.ID
	if _, e = s.DB.Exec("DELETE FROM asset_projects WHERE asset_id=?", replacement); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec("DELETE FROM assets WHERE id=?", replacement); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec("UPDATE assets SET path=?,status='active' WHERE id=?", a.Path, a.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec("UPDATE file_operations SET stage='prepared',receipt=NULL WHERE project_id=? AND request_id='edit'", p); e != nil {
		t.Fatal(e)
	}
	if _, e = s2.Mutate(ctx, p, "content", content); e != nil {
		t.Fatal(e)
	}
	f, e = s.Workspace.OpenRegular(p, a.Path)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := io.ReadAll(f)
	f.Close()
	if string(b) != "updated" {
		t.Fatal(string(b))
	}
}
func TestFilesBulkValidationAndVersionProtection(t *testing.T) {
	s, p := setup(t)
	ctx := context.Background()
	a := put(t, s, p, "a", "a.txt", "one")
	if _, e := s.Mutate(ctx, p, "delete", Input{RequestID: "badbulk", Paths: []string{a.Path, prefix(p) + "files/absent"}}); e == nil {
		t.Fatal("accepted invalid bulk")
	}
	f, e := s.Workspace.OpenRegular(p, a.Path)
	if e != nil {
		t.Fatal("bulk removed first file", e)
	}
	f.Close()
	if _, e = s.DB.Exec("INSERT INTO versions(id,resource_id,asset_id,created_at) VALUES('v','r',?,0)", a.ID); e != nil {
		t.Fatal(e)
	}
	for _, op := range []string{"delete", "content"} {
		in := Input{RequestID: op, Path: a.Path, Paths: []string{a.Path}, Content: "x"}
		if _, e = s.Mutate(ctx, p, op, in); e == nil {
			t.Fatalf("accepted versioned %s", op)
		}
	}
	f, e = s.Workspace.OpenRegular(p, a.Path)
	if e != nil {
		t.Fatal(e)
	}
	f.Close()
	if _, e = s.Mutate(ctx, p, "rename", Input{RequestID: "private", Path: prefix(p) + "staging/secret", NewName: "x"}); e == nil {
		t.Fatal("private mutation")
	}
}
func TestFilesRenameCollisionAndFingerprintVector(t *testing.T) {
	s, p := setup(t)
	ctx := context.Background()
	a := put(t, s, p, "one", "a.txt", "abc")
	f, e := s.Workspace.OpenRegular(p, a.Path)
	if e != nil {
		t.Fatal(e)
	}
	fp, e := Fingerprint(f, true)
	f.Close()
	if e != nil || fp.FullHash != "78af5f94892f3950" || fp.QuickHash != fp.FullHash {
		t.Fatalf("XXH3 abc vector %+v %v", fp, e)
	}
	if _, e = s.Mutate(ctx, p, "content", Input{RequestID: "other", Path: path.Dir(a.Path) + "/taken.txt", Content: "keep"}); e != nil {
		t.Fatal(e)
	}
	out, e := s.Mutate(ctx, p, "rename", Input{RequestID: "rename", Path: a.Path, NewName: "taken.txt"})
	if e != nil || !strings.Contains(string(out), "taken (1).txt") {
		t.Fatalf("collision suffix %s %v", out, e)
	}
	f, e = s.Workspace.OpenRegular(p, path.Dir(a.Path)+"/taken.txt")
	if e != nil {
		t.Fatal(e)
	}
	b, _ := io.ReadAll(f)
	f.Close()
	if string(b) != "keep" {
		t.Fatal("overwrote collision")
	}
}
func TestFingerprintLargePrefix(t *testing.T) {
	s, p := setup(t)
	a := put(t, s, p, "large1", "a.bin", strings.Repeat("a", 4<<20)+"x")
	b := put(t, s, p, "large2", "b.bin", strings.Repeat("a", 4<<20)+"y")
	f, _ := s.Workspace.OpenRegular(p, a.Path)
	x, e := Fingerprint(f, true)
	f.Close()
	if e != nil {
		t.Fatal(e)
	}
	f, _ = s.Workspace.OpenRegular(p, b.Path)
	y, e := Fingerprint(f, true)
	f.Close()
	if e != nil || x.QuickHash != y.QuickHash || x.FullHash == y.FullHash {
		t.Fatalf("prefix/full mismatch %+v %+v %v", x, y, e)
	}
}
func TestFilesJunctionAndHardlink(t *testing.T) {
	s, p := setup(t)
	a := put(t, s, p, "a", "a.txt", "safe")
	external := t.TempDir()
	os.WriteFile(filepath.Join(external, "outside.txt"), []byte("secret"), 0600)
	root := s.Workspace.root.Name()
	link := filepath.Join(root, "projects", p, "files", "junction")
	if _, e := exec.Command("cmd", "/c", "mklink", "/J", link, external).CombinedOutput(); e != nil {
		t.Fatal("junction setup", e)
	}
	if f, e := s.Workspace.OpenRegular(p, prefix(p)+"files/junction/outside.txt"); e == nil {
		f.Close()
		t.Fatal("junction escaped")
	}
	hard := filepath.Join(root, "projects", p, "files", "hard.txt")
	if e := os.Link(filepath.Join(root, filepath.FromSlash(a.Path)), hard); e != nil {
		t.Fatal(e)
	}
	if f, e := s.Workspace.OpenRegular(p, a.Path); e == nil {
		f.Close()
		t.Fatal("accepted hardlink")
	}
	for _, n := range []string{"COM¹.txt", "LPT²", "NUL.txt", "a ", "a.", "C:ads"} {
		if _, e := s.Stage(context.Background(), p, "invalid", n, strings.NewReader("a")); e == nil {
			t.Errorf("accepted %q", n)
		}
	}
}

func TestFilesCopyPreparedRecoveryAndRegister(t *testing.T) {
	s, p := setup(t)
	ctx := context.Background()
	a := put(t, s, p, "a", "a.txt", "hello")
	registered, e := s.Register(ctx, p, "register", a.Path)
	if e != nil || registered.ID != a.ID || registered.FullHash == nil {
		t.Fatalf("register %+v %v", registered, e)
	}
	if _, e = s.Register(ctx, p, "register", prefix(p)+"files/else"); !errors.Is(e, store.ErrRequestConflict) {
		t.Fatalf("register conflict %v", e)
	}
	target := prefix(p) + "files/copy"
	if _, e = s.Mutate(ctx, p, "mkdir", Input{RequestID: "mkdir", Path: target}); e != nil {
		t.Fatal(e)
	}
	in := Input{RequestID: "recovercopy", Paths: []string{a.Path}, Target: target}
	j, e := s.load(ctx, p, in.RequestID, "copy", in)
	if e != nil {
		t.Fatal(e)
	}
	f, e := s.Workspace.OpenRegular(p, a.Path)
	if e != nil {
		t.Fatal(e)
	}
	fp, e := Fingerprint(f, false)
	if e != nil {
		t.Fatal(e)
	}
	copyAsset := asset(uuid.NewString(), target+"/a.txt", fp)
	j.Plan = []move{{Source: a.Path, Target: copyAsset.Path, Dev: fp.DevID, Inode: fp.Inode, Asset: &copyAsset}}
	j.Stage = "prepared"
	if e = s.save(ctx, p, in.RequestID, j); e != nil {
		t.Fatal(e)
	}
	if e = s.prepareBytes(ctx, p, in.RequestID, j, &j.Plan[0], f, s.MaxUpload); e != nil {
		t.Fatal(e)
	}
	f.Close()
	if _, e = NewService(s.DB, s.Workspace).Mutate(ctx, p, "copy", in); e != nil {
		t.Fatal(e)
	}
	f, e = s.Workspace.OpenRegular(p, copyAsset.Path)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := io.ReadAll(f)
	f.Close()
	if string(b) != "hello" {
		t.Fatal(string(b))
	}
}
func TestFilesPendingGuardAndInterruptedPreparation(t *testing.T) {
	s, p := setup(t)
	ctx := context.Background()
	a := put(t, s, p, "a", "a.txt", "old")
	in := Input{RequestID: "partial", Path: a.Path, Content: "new"}
	j, e := s.load(ctx, p, in.RequestID, "content", in)
	if e != nil {
		t.Fatal(e)
	}
	old, e := s.planMove(p, a.Path, prefix(p)+"recycle/old/a.txt")
	if e != nil {
		t.Fatal(e)
	}
	id := uuid.NewString()
	j.Plan = []move{old, {Source: prefix(p) + "staging/new/a.txt", Target: a.Path, Asset: &store.Asset{ID: id}}}
	j.Stage = "prepared"
	if e = s.save(ctx, p, in.RequestID, j); e != nil {
		t.Fatal(e)
	}
	tx, e := s.DB.Begin()
	if e != nil {
		t.Fatal(e)
	}
	e = store.CheckAssetFilePending(ctx, tx, a.ID)
	tx.Rollback()
	if !errors.Is(e, store.ErrRequestPending) {
		t.Fatalf("pending guard %v", e)
	}
	if e = s.prepareBytes(ctx, p, in.RequestID, j, &j.Plan[1], brokenReader{}, s.MaxUpload); e == nil {
		t.Fatal("partial did not fail")
	}
	if _, e = NewService(s.DB, s.Workspace).Mutate(ctx, p, "content", in); e != nil {
		t.Fatal(e)
	}
}
func TestFilesCommitPreparedRecovery(t *testing.T) {
	s, p := setup(t)
	ctx := context.Background()
	v, e := s.Stage(ctx, p, "up", "a.txt", strings.NewReader("abc"))
	if e != nil {
		t.Fatal(e)
	}
	a, e := s.Commit(ctx, p, "commit", v)
	if e != nil {
		t.Fatal(e)
	}
	s.DB.Exec("DELETE FROM asset_projects WHERE asset_id=?", a.ID)
	s.DB.Exec("DELETE FROM assets WHERE id=?", a.ID)
	s.DB.Exec("UPDATE file_operations SET stage='prepared',receipt=NULL WHERE project_id=? AND request_id='commit'", p)
	b, e := NewService(s.DB, s.Workspace).Commit(ctx, p, "commit", v)
	if e != nil || a.ID != b.ID {
		t.Fatalf("commit recovery %+v %v", b, e)
	}
}

func TestFilesHeldMutationParents(t *testing.T) {
	s, p := setup(t)
	a := put(t, s, p, "a", "a.txt", "hello")
	rel, _ := relative(p, a.Path)
	parent, release, e := s.Workspace.heldParent(p, path.Dir(rel))
	if e != nil {
		t.Fatal(e)
	}
	root := s.Workspace.root.Name()
	dir := filepath.Join(root, filepath.FromSlash(path.Dir(a.Path)))
	if e = os.Rename(dir, dir+"-outside"); e == nil {
		release()
		t.Fatal("held parent moved")
	}
	f, e := nativeOpen(parent, path.Base(a.Path), false, false, false)
	if e != nil {
		release()
		t.Fatal(e)
	}
	if e = os.Rename(filepath.Join(root, filepath.FromSlash(a.Path)), filepath.Join(root, filepath.FromSlash(a.Path))+"-other"); e == nil {
		f.Close()
		release()
		t.Fatal("held source replaced")
	}
	if e = nativeRename(f, parent, "safe.txt"); e != nil {
		f.Close()
		release()
		t.Fatal("owned rename", e)
	}
	f.Close()
	release()
	if f, e = s.Workspace.OpenRegular(p, path.Dir(a.Path)+"/safe.txt"); e != nil {
		t.Fatal(e)
	}
	f.Close()
}

func TestFilesChangedStagingRejected(t *testing.T) {
	s, p := setup(t)
	ctx := context.Background()
	v, e := s.Stage(ctx, p, "up", "a.txt", strings.NewReader("old"))
	if e != nil {
		t.Fatal(e)
	}
	absolute := filepath.Join(s.Workspace.root.Name(), filepath.FromSlash(v))
	st, e := os.Stat(absolute)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(absolute, []byte("new"), 0600); e != nil {
		t.Fatal(e)
	}
	os.Chtimes(absolute, st.ModTime(), st.ModTime())
	if _, e = s.Commit(ctx, p, "commit", v); e == nil {
		t.Fatal("changed staging accepted")
	}
}

func TestFilesConcurrentReadHandles(t *testing.T) {
	s, p := setup(t)
	a := put(t, s, p, "read", "a.txt", "hello")
	first, e := s.Workspace.OpenRegular(p, a.Path)
	if e != nil {
		t.Fatal(e)
	}
	defer first.Close()
	second, e := s.Workspace.OpenRegular(p, a.Path)
	if e != nil {
		t.Fatal("parallel readers must share", e)
	}
	defer second.Close()
}

func TestFilesRenameRespectsPreparedDestinations(t *testing.T) {
	for _, suffix := range []bool{false, true} {
		t.Run(fmt.Sprintf("suffix_%v", suffix), func(t *testing.T) {
			s, p := setup(t)
			ctx := context.Background()
			a := put(t, s, p, "source", "a.txt", "source")
			name := "reserved.txt"
			target := path.Join(path.Dir(a.Path), name)
			if suffix {
				if _, e := s.Mutate(ctx, p, "content", Input{RequestID: "existing", Path: target, Content: "existing"}); e != nil {
					t.Fatal(e)
				}
				target = path.Join(path.Dir(a.Path), "reserved (1).txt")
			}
			in := Input{RequestID: "original", Path: target, Content: "original"}
			j, e := s.load(ctx, p, in.RequestID, "content", in)
			if e != nil {
				t.Fatal(e)
			}
			j.Stage = "prepared"
			j.Plan = []move{{Source: prefix(p) + "staging/original/reserved.txt", Target: target, Asset: &store.Asset{ID: uuid.NewString()}}}
			if e = s.save(ctx, p, in.RequestID, j); e != nil {
				t.Fatal(e)
			}
			_, e = s.Mutate(ctx, p, "rename", Input{RequestID: "rename", Path: a.Path, NewName: name})
			if !errors.Is(e, store.ErrRequestPending) {
				t.Errorf("rename must respect reserved destination: %v", e)
			}
			f, e := s.Workspace.OpenRegular(p, a.Path)
			if e != nil {
				t.Errorf("source moved despite reservation: %v", e)
			} else {
				f.Close()
			}
			// A rejected suffix must not replace the rename's previously published plan.
			var plans string
			e = s.DB.QueryRowContext(ctx, "SELECT source FROM file_operations WHERE project_id=? AND request_id='rename'", p).Scan(&plans)
			if suffix {
				if e != nil {
					t.Fatal(e)
				}
				var moves []move
				if e = json.Unmarshal([]byte(plans), &moves); e != nil {
					t.Fatal(e)
				}
				if moves[0].Target == target {
					t.Error("reserved suffix was published")
				}
			} else if !errors.Is(e, sql.ErrNoRows) {
				t.Errorf("reserved initial plan published: %v", e)
			}
			if _, e = NewService(s.DB, s.Workspace).Mutate(ctx, p, "content", in); e != nil {
				t.Fatalf("original operation no longer recoverable: %v", e)
			}
			f, e = s.Workspace.OpenRegular(p, target)
			if e != nil {
				t.Fatal(e)
			}
			b, e := io.ReadAll(f)
			f.Close()
			if e != nil || string(b) != "original" {
				t.Fatalf("original bytes %q %v", b, e)
			}
		})
	}
}
func TestFilesFirstIntentProjectDeletionInterleaving(t *testing.T) {
	s, p := setup(t)
	ctx := context.Background()
	in := Input{RequestID: "first", Path: prefix(p) + "files/new"}
	// Pause exactly after initial scope/load, before the first journal transaction.
	j, e := s.load(ctx, p, in.RequestID, "mkdir", in)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = (store.Objects{DB: s.DB}).Mutate(ctx, p, "projects", store.Mutation{RequestID: "delete", Action: "delete", ID: p}); e != nil {
		t.Fatal(e)
	}
	j.Stage = "prepared"
	j.Plan = []move{{Target: in.Path, Directory: true}}
	e = s.save(ctx, p, in.RequestID, j)
	var domain *store.DomainError
	if !errors.As(e, &domain) || domain.Code != "not_found" {
		t.Errorf("first save after project deletion: %v", e)
	}
	var count int
	if e = s.DB.QueryRowContext(ctx, "SELECT count(*) FROM file_operations WHERE project_id=?", p).Scan(&count); e != nil {
		t.Fatal(e)
	}
	if count != 0 {
		t.Fatalf("dangling journal count %d", count)
	}
	if _, e = os.Stat(filepath.Join(s.Workspace.root.Name(), "projects", p)); !errors.Is(e, os.ErrNotExist) {
		t.Fatalf("filesystem published after deletion: %v", e)
	}
}
