package workspace

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

type Workspace struct{ root *os.Root }

func Open(name string) (*Workspace, error) {
	info, e := os.Lstat(name)
	if e != nil {
		return nil, e
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, invalid("Workspace root must be a real directory")
	}
	r, e := os.OpenRoot(name)
	if e != nil {
		return nil, e
	}
	f, e := r.Open(".")
	if e == nil {
		e = checkHandle(f, false)
		f.Close()
	}
	if e != nil {
		r.Close()
		return nil, e
	}
	return &Workspace{root: r}, nil
}
func (w *Workspace) Close() error { return w.root.Close() }
func validPath(v string) error {
	if !filepath.IsLocal(v) || v == "" || !utf8.ValidString(v) || strings.ContainsAny(v, "\\:\x00") || strings.HasPrefix(v, "/") || path.Clean(v) != v {
		return invalid("Invalid relative path")
	}
	for _, s := range strings.Split(v, "/") {
		if s == "." || s == ".." || s == "" || strings.TrimRight(s, " .") != s || strings.ContainsAny(s, "<>\"|?*") {
			return invalid("Invalid filename")
		}
		for _, c := range s {
			if c < 32 {
				return invalid("Invalid filename")
			}
		}

	}
	return nil
}
func validLeaf(v string) error {
	if strings.Contains(v, "/") {
		return invalid("Expected one filename")
	}
	return validPath(v)
}
func prefix(p string) string { return "projects/" + p + "/" }
func relative(p, v string) (string, error) {
	if e := validPath(v); e != nil {
		return "", e
	}
	if !strings.HasPrefix(v, prefix(p)) {
		return "", invalid("Path belongs to another project")
	}
	v = strings.TrimPrefix(v, prefix(p))
	if v == "" {
		return "", invalid("Project root is not a file")
	}
	return v, nil
}
func (w *Workspace) open(p, v string, regular bool) (*os.File, error) {
	return w.openAccess(p, v, regular, true)
}
func (w *Workspace) openAccess(p, v string, regular, mutation bool) (*os.File, error) {
	rel, e := relative(p, v)
	if e != nil {
		return nil, e
	}
	parent, release, e := w.heldParent(p, path.Dir(rel))
	if e != nil {
		return nil, e
	}
	defer release()
	f, e := nativeAccess(parent, path.Base(rel), false, false, false, mutation)
	if e != nil {
		return nil, e
	}
	if e = checkHandle(f, regular); e != nil {
		f.Close()
		return nil, e
	}
	return f, nil
}
func (w *Workspace) OpenRegular(p, v string) (*os.File, error) {
	return w.openAccess(p, v, true, false)
}
func (w *Workspace) mkdir(p, v string) error {
	rel, e := relative(p, v)
	if e != nil {
		return e
	}
	_, release, e := w.heldDirectory(p, rel, true)
	if e != nil {
		return e
	}
	release()
	return nil
}
func (w *Workspace) create(p, v string) (*os.File, error) {
	rel, e := relative(p, v)
	if e != nil {
		return nil, e
	}
	parent, release, e := w.heldParent(p, path.Dir(rel))
	if e != nil {
		return nil, e
	}
	defer release()
	return nativeOpen(parent, path.Base(rel), false, true, false)
}
func (w *Workspace) rename(p, src, dst string, expected *os.File) error {
	rel, e := relative(p, dst)
	if e != nil {
		return e
	}
	parent, release, e := w.heldParent(p, path.Dir(rel))
	if e != nil {
		return e
	}
	defer release()
	if expected == nil {
		return errors.New("rename requires held source handle")
	}
	return nativeRename(expected, parent, path.Base(rel))
}

// Retain every ancestor through mutation. Native directory handles omit
// FILE_SHARE_DELETE, preventing another process from moving the parent chain.
func (w *Workspace) heldParent(p, rel string) (*os.File, func(), error) {
	return w.heldDirectory(p, rel, false)
}
func (w *Workspace) heldDirectory(p, rel string, create bool) (*os.File, func(), error) {
	if e := validLeaf(p); e != nil {
		return nil, nil, e
	}
	if rel != "." {
		if e := validPath(rel); e != nil {
			return nil, nil, e
		}
	}
	parts := []string{"projects", p}
	if rel != "." {
		parts = append(parts, strings.Split(rel, "/")...)
	}
	return w.heldPath(parts, create)
}

func (w *Workspace) heldPath(parts []string, create bool) (*os.File, func(), error) {
	root, e := w.root.Open(".")
	if e != nil {
		return nil, nil, e
	}
	held := []*os.File{root}
	release := func() {
		for i := len(held) - 1; i >= 0; i-- {
			held[i].Close()
		}
	}
	parent := root
	for _, part := range parts {
		f, e := nativeOpen(parent, part, true, create, create)
		if e != nil {
			release()
			return nil, nil, e
		}
		held = append(held, f)
		parent = f
	}
	return parent, release, nil
}

// ManagedRead and ManagedInstall reuse the same held, non-reparse ancestor chain.
// They are internal storage operations, never a native agent filesystem tool.
func (w *Workspace) ManagedRead(name string, limit int64) ([]byte, error) {
	if e := validPath(name); e != nil {
		return nil, e
	}
	parts := strings.Split(name, "/")
	parent, release, e := w.heldPath(parts[:len(parts)-1], false)
	if e != nil {
		return nil, e
	}
	defer release()
	f, e := nativeAccess(parent, parts[len(parts)-1], false, false, false, false)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	if e = checkHandle(f, true); e != nil {
		return nil, e
	}
	b, e := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(b)) > limit {
		return nil, invalid("Managed file exceeds size limit")
	}
	return b, e
}
func (w *Workspace) ManagedInstall(name string, content []byte) error {
	if e := validPath(name); e != nil {
		return e
	}
	parts := strings.Split(name, "/")
	parent, release, e := w.heldPath(parts[:len(parts)-1], true)
	if e != nil {
		return e
	}
	defer release()
	f, e := nativeOpen(parent, parts[len(parts)-1], false, true, false)
	if errors.Is(e, os.ErrExist) {
		f, e = nativeAccess(parent, parts[len(parts)-1], false, false, false, false)
		if e != nil {
			return e
		}
		defer f.Close()
		return checkHandle(f, true)
	}
	if e != nil {
		return e
	}
	return writeClosed(f, strings.NewReader(string(content)), int64(len(content)))
}
func writeClosed(f *os.File, r io.Reader, limit int64) error {
	_, e := io.Copy(f, io.LimitReader(r, limit+1))
	if e == nil {
		st, se := f.Stat()
		e = se
		if e == nil && st.Size() > limit {
			e = invalid("Upload exceeds size limit")
		}
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	return e
}
func collisionName(name string, n int) string {
	if n == 0 {
		return name
	}
	ext := path.Ext(name)
	return fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(name, ext), n, ext)
}
