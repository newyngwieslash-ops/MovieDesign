package tasks

import (
	"context"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"testing"
)

func TestBillingIsNotASettledWait(t *testing.T) {
	if SettlesAgentWait(WaitingUser, "billing") {
		t.Fatal("billing wait ended early")
	}
	if !SettlesAgentWait(WaitingUser, "choice") {
		t.Fatal("ordinary decision blocked agent")
	}
}

// Mutating registered metadata after the card was frozen must invalidate approval.
func TestBillingRejectsChangedReferenceSnapshot(t *testing.T) {
	s, p, in := setup(t)
	ctx := context.Background()
	if _, e := s.DB.Exec("INSERT INTO assets(id,path,size,mtime_ms,name,created_at,updated_at)VALUES('ref','ref.png',1,2,'Image',3,4)"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.DB.Exec("INSERT INTO asset_projects(asset_id,project_id)VALUES('ref',?)", p); e != nil {
		t.Fatal(e)
	}
	in.References = []Reference{{AssetID: "ref", Role: "reference"}}
	in.Assets = []providers.ReferenceAsset{{Asset: store.Asset{ID: "ref", Path: "ref.png", Status: "active", Size: 1, MtimeMS: 2, Name: "Image", CreatedAt: 3, UpdatedAt: 4}, Role: "reference"}}
	v, e := s.Submit(ctx, p, in)
	if e != nil {
		t.Fatal(e)
	}
	check := func() error {
		tx, e := s.DB.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		_, e = s.BillingInput(ctx, tx, p, "", v.ID, v.Revision)
		return e
	}
	if e = check(); e != nil {
		t.Fatal("unchanged snapshot", e)
	}
	if _, e = s.DB.Exec("UPDATE assets SET name='Changed' WHERE id='ref'"); e != nil {
		t.Fatal(e)
	}
	if e = check(); e == nil {
		t.Fatal("changed registered input snapshot still eligible for approval")
	}
}
