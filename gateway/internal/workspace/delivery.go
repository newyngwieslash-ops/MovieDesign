package workspace

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path"
	"strings"

	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
)

// InspectDelivery captures a confined ordinary file without registering SQL state.
func (w *Workspace) InspectDelivery(p, v, id string) (store.Asset, error) {
	var zero store.Asset
	if _, e := relative(p, v); e != nil {
		return zero, e
	}
	if !strings.HasPrefix(v, prefix(p)+"files/") && !strings.HasPrefix(v, prefix(p)+"staging/") {
		return zero, invalid("Expected a project files or staging path")
	}
	f, e := w.OpenRegular(p, v)
	if e != nil {
		return zero, e
	}
	defer f.Close()
	fp, e := Fingerprint(f, true)
	if e != nil {
		return zero, e
	}
	a := asset(id, v, fp)
	a.FullHash = &fp.FullHash
	return a, nil
}

// ArchiveDelivery replays a no-replace move using the frozen identity and digest.
// Registered files use source==target and are verified in place.
func (w *Workspace) ArchiveDelivery(p, source, target string, a store.Asset) error {
	if a.DevID == nil || a.Inode == nil || a.FullHash == nil {
		return invalid("Missing archive fingerprint")
	}
	if source == target {
		return w.VerifyDelivery(p, target, a)
	}
	if e := w.mkdir(p, path.Dir(target)); e != nil {
		return e
	}
	// A foreign destination is never overwritten, even if its bytes happen to match.
	if f, e := w.OpenRegular(p, target); e == nil {
		f.Close()
		return w.VerifyDelivery(p, target, a)
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	return (&Service{Workspace: w}).applyMove(p, move{Source: source, Target: target, Dev: *a.DevID, Inode: *a.Inode, Digest: *a.FullHash})
}
func (w *Workspace) VerifyDelivery(p, v string, a store.Asset) error {
	f, e := w.OpenRegular(p, v)
	if e != nil {
		return e
	}
	defer f.Close()
	fp, e := Fingerprint(f, true)
	if e != nil {
		return e
	}
	if a.DevID == nil || a.Inode == nil || a.FullHash == nil || fp.DevID != *a.DevID || fp.Inode != *a.Inode || fp.FullHash != *a.FullHash || fp.Size != a.Size {
		return invalid("Delivery file identity or content changed")
	}
	return nil
}

// InsertDeliveryAsset participates in the caller's whole-batch SQL transaction.
func InsertDeliveryAsset(ctx context.Context, tx *sql.Tx, p string, a store.Asset) error {
	return insertAsset(ctx, tx, p, a)
}
