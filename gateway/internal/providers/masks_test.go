package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/workspace"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImageMaskSourcePixelsAndReceipt(t *testing.T) {
	s, p := imageService(t)
	ctx := context.Background()
	stage, e := s.Files.Stage(ctx, p, "source-upload", "source.png", bytes.NewReader(tinyPNG(t)))
	if e != nil {
		t.Fatal(e)
	}
	source, e := s.Files.Commit(ctx, p, "source-commit", stage)
	if e != nil {
		t.Fatal(e)
	}
	in := MaskInput{RequestID: "mask-one", AssetID: source.ID, Bounds: []MaskBounds{{X: 0, Y: 0, Width: 1, Height: 1}}}
	mask, e := s.CreateMask(ctx, p, in)
	if e != nil {
		t.Fatal(e)
	}
	f, e := s.Files.Workspace.OpenRegular(p, mask.Path)
	if e != nil {
		t.Fatal(e)
	}
	decoded, _, e := image.Decode(f)
	f.Close()
	if e != nil {
		t.Fatal(e)
	}
	_, _, _, inside := decoded.At(0, 0).RGBA()
	_, _, _, outside := decoded.At(1, 1).RGBA()
	if inside != 0 || outside != 65535 {
		t.Fatal("mask alpha wrong", inside, outside)
	}
	// A legacy intent can still replay its authoritative completed mask receipt.
	if _, e = s.DB.Exec("UPDATE write_requests SET response_json='{}' WHERE project_id=? AND operation='generate.mask.intent' AND request_id=?", p, in.RequestID); e != nil {
		t.Fatal(e)
	}
	again, e := s.CreateMask(ctx, p, in)
	if e != nil || again.ID != mask.ID {
		t.Fatal("mask not idempotent", e)
	}
	in.Bounds[0].Width = .5
	if _, e = s.CreateMask(ctx, p, in); e == nil {
		t.Fatal("same pixels but changed original geometry must conflict")
	}
	in.RequestID = "outside-mask"
	in.Bounds[0].Width = 3
	if _, e = s.CreateMask(ctx, p, in); e == nil {
		t.Fatal("out-of-bounds mask accepted")
	}
}

func TestImageMaskCommitRecoveryBeforeSource(t *testing.T) {
	for _, change := range []string{"missing", "replaced"} {
		t.Run(change, func(t *testing.T) {
			s, p := imageService(t)
			root := t.TempDir()
			w, e := workspace.Open(root)
			if e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() { w.Close() })
			s.Files = workspace.NewService(s.DB, w)
			ctx := context.Background()
			stage, e := s.Files.Stage(ctx, p, "recovery-source-upload", "source.png", bytes.NewReader(tinyPNG(t)))
			if e != nil {
				t.Fatal(e)
			}
			source, e := s.Files.Commit(ctx, p, "recovery-source-commit", stage)
			if e != nil {
				t.Fatal(e)
			}
			in := MaskInput{RequestID: "recover-mask", AssetID: source.ID, Bounds: []MaskBounds{{Width: 1, Height: 1}}}
			if _, e = s.DB.Exec(`CREATE TRIGGER fail_mask_receipt BEFORE INSERT ON write_requests WHEN NEW.operation='generate.mask' BEGIN SELECT RAISE(ABORT,'injected final receipt failure'); END`); e != nil {
				t.Fatal(e)
			}
			if _, e = s.CreateMask(ctx, p, in); e == nil {
				t.Fatal("failure injection missed")
			}
			var receipt string
			if e = s.DB.QueryRow("SELECT f.receipt FROM file_operations f JOIN write_requests r ON r.project_id=f.project_id AND f.request_id=json_extract(r.response_json,'$.commit') WHERE r.project_id=? AND r.operation='generate.mask.intent' AND r.request_id='recover-mask' AND f.stage='completed'", p).Scan(&receipt); e != nil {
				t.Fatal("mask did not commit", e)
			}
			var original struct {
				ID string `json:"id"`
			}
			if e = json.Unmarshal([]byte(receipt), &original); e != nil {
				t.Fatal(e)
			}
			if _, e = s.DB.Exec("DROP TRIGGER fail_mask_receipt"); e != nil {
				t.Fatal(e)
			}
			if change == "missing" {
				e = os.Remove(filepath.Join(root, filepath.FromSlash(source.Path)))
			} else {
				var replacement bytes.Buffer
				if e = png.Encode(&replacement, image.NewNRGBA(image.Rect(0, 0, 3, 3))); e != nil {
					t.Fatal(e)
				}
				e = os.WriteFile(filepath.Join(root, filepath.FromSlash(source.Path)), replacement.Bytes(), 0600)
			}
			if e != nil {
				t.Fatal(e)
			}
			recovered, e := s.CreateMask(ctx, p, in)
			if e != nil || recovered.ID != original.ID {
				t.Fatalf("committed mask recovery depends on source: %+v %v", recovered, e)
			}
			var count int
			s.DB.QueryRow("SELECT count(*) FROM assets a JOIN asset_projects p ON p.asset_id=a.id WHERE p.project_id=?", p).Scan(&count)
			if count != 2 {
				t.Fatal("duplicate mask asset", count)
			}
			in.Bounds[0].Width = .5
			if _, e = s.CreateMask(ctx, p, in); e == nil {
				t.Fatal("recovered mask ignored original intent")
			}
		})
	}
}

func TestImageMaskUploadedRecoveryBeforeSource(t *testing.T) {
	s, p := imageService(t)
	ctx := context.Background()
	stage, e := s.Files.Stage(ctx, p, "source-upload", "source.png", bytes.NewReader(tinyPNG(t)))
	if e != nil {
		t.Fatal(e)
	}
	source, e := s.Files.Commit(ctx, p, "source-commit", stage)
	if e != nil {
		t.Fatal(e)
	}
	in := MaskInput{RequestID: "recover-upload", AssetID: source.ID, Bounds: []MaskBounds{{Width: 1, Height: 1}}}
	if _, e = s.DB.Exec(`CREATE TRIGGER fail_mask_commit BEFORE INSERT ON file_operations WHEN NEW.operation='commit' BEGIN SELECT RAISE(ABORT,'injected commit preparation failure'); END`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.CreateMask(ctx, p, in); e == nil {
		t.Fatal("failure injection missed")
	}
	if _, e = s.DB.Exec("DROP TRIGGER fail_mask_commit"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec("UPDATE assets SET status='missing' WHERE id=?", source.ID); e != nil {
		t.Fatal(e)
	}
	// Simulate the durable uploaded checkpoint before Stage writes its receipt.
	if _, e = s.DB.Exec("UPDATE file_operations SET stage='uploaded',receipt=NULL WHERE project_id=? AND request_id=(SELECT json_extract(response_json,'$.upload') FROM write_requests WHERE project_id=? AND operation='generate.mask.intent' AND request_id='recover-upload')", p, p); e != nil {
		t.Fatal(e)
	}
	mask, e := s.CreateMask(ctx, p, in)
	if e != nil || mask.ID == "" {
		t.Fatal("uploaded mask recovery depends on source", e)
	}
}

func TestImageMaskRejectsUnrelatedJournals(t *testing.T) {
	for _, operation := range []string{"commit", "upload"} {
		t.Run(operation, func(t *testing.T) {
			s, p := imageService(t)
			ctx := context.Background()
			stage, e := s.Files.Stage(ctx, p, "mask-upload-collision", "ordinary.txt", bytes.NewBufferString("ordinary user file"))
			if e != nil {
				t.Fatal(e)
			}
			if operation == "commit" {
				if _, e = s.Files.Commit(ctx, p, "mask-commit-collision", stage); e != nil {
					t.Fatal(e)
				}
			}
			in := MaskInput{RequestID: "collision", AssetID: "unavailable-source", Bounds: []MaskBounds{{Width: 1, Height: 1}}}
			mask, e := s.CreateMask(ctx, p, in)
			if e == nil || mask.ID != "" {
				t.Fatalf("adopted unrelated %s as mask: %+v %v", operation, mask, e)
			}
			var count int
			if e = s.DB.QueryRow("SELECT count(*) FROM write_requests WHERE project_id=? AND operation='generate.mask'", p).Scan(&count); e != nil || count != 0 {
				t.Fatal("invalid mask received final receipt", count, e)
			}
			if _, e = s.DB.Exec("UPDATE write_requests SET response_json='{}' WHERE project_id=? AND operation='generate.mask.intent' AND request_id=?", p, in.RequestID); e != nil {
				t.Fatal(e)
			}
			if _, e = s.CreateMask(ctx, p, in); e == nil || !strings.Contains(e.Error(), "lacks owned journals") {
				t.Fatal("legacy incomplete intent must not infer ownership", e)
			}
		})
	}
}
