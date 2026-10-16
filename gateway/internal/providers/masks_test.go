package providers

import (
	"bytes"
	"context"
	"image"
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
