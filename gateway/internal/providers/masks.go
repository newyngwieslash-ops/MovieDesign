package providers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
)

type MaskBounds struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}
type MaskInput struct {
	RequestID    string          `json:"requestId"`
	AssetID      string          `json:"asset_id"`
	Bounds       []MaskBounds    `json:"bounds"`
	OriginalWire json.RawMessage `json:"-"`
}

// CreateMask registers a source-pixel rectangle mask through the existing journal.
func (s *Service) CreateMask(ctx context.Context, p string, in MaskInput) (store.Asset, error) {
	var zero store.Asset
	if s.Files == nil {
		return zero, ErrUnavailable
	}
	if len(in.Bounds) < 1 || len(in.Bounds) > 100 || !identifier.MatchString(in.RequestID) || len(in.RequestID) > 100 {
		return zero, invalid("Invalid mask request")
	}
	wire := in.OriginalWire
	if wire == nil {
		wire, _ = json.Marshal(in)
	}
	canonical, e := store.CanonicalJSON(wire)
	if e != nil {
		return zero, e
	}
	// Reserve the complete mask intent before any managed upload. Replay shares
	// the existing upload/commit journals and never runs file I/O inside a SQL tx.
	if _, e = store.WriteRequest(ctx, s.DB, store.Scope{ProjectID: p, Operation: "generate.mask.intent", RequestID: in.RequestID}, canonical, func(tx *sql.Tx) ([]byte, error) { return []byte(`{}`), nil }); e != nil {
		return zero, e
	}
	var receipt string
	e = s.DB.QueryRowContext(ctx, "SELECT response_json FROM write_requests WHERE project_id=? AND operation='generate.mask' AND request_id=? AND state='completed'", p, in.RequestID).Scan(&receipt)
	if e == nil {
		e = json.Unmarshal([]byte(receipt), &zero)
		return zero, e
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return zero, e
	}
	raw, e := (store.Objects{DB: s.DB}).Get(ctx, p, "assets", in.AssetID)
	if e != nil {
		return zero, e
	}
	var a store.Asset
	if json.Unmarshal(raw, &a) != nil || a.Status != "active" {
		return zero, invalid("Source image unavailable")
	}
	file, e := s.Files.Workspace.OpenRegular(p, a.Path)
	if e != nil {
		return zero, e
	}
	data, e := readBounded(file, 20<<20)
	file.Close()
	if e != nil {
		return zero, e
	}
	cap := Capability{Constraints: Constraints{MaxImageBytes: 20 << 20, MaxPixels: 16777216}}
	c, _, e := imageBytes(data, cap)
	if e != nil {
		return zero, e
	}
	mask := image.NewNRGBA(image.Rect(0, 0, c.Width, c.Height))
	draw.Draw(mask, mask.Bounds(), image.NewUniform(color.NRGBA{A: 255}), image.Point{}, draw.Src)
	for _, b := range in.Bounds {
		if math.IsNaN(b.X) || math.IsNaN(b.Y) || math.IsNaN(b.Width) || math.IsNaN(b.Height) || math.IsInf(b.X, 0) || math.IsInf(b.Y, 0) || math.IsInf(b.Width, 0) || math.IsInf(b.Height, 0) || b.X < 0 || b.Y < 0 || b.Width <= 0 || b.Height <= 0 || b.X+b.Width > float64(c.Width) || b.Y+b.Height > float64(c.Height) {
			return zero, invalid("Mask bounds must be finite source-pixel rectangles within image")
		}
		rectangle := image.Rect(int(math.Floor(b.X)), int(math.Floor(b.Y)), int(math.Ceil(b.X+b.Width)), int(math.Ceil(b.Y+b.Height)))
		draw.Draw(mask, rectangle, image.Transparent, image.Point{}, draw.Src)
	}
	var encoded bytes.Buffer
	if e = png.Encode(&encoded, mask); e != nil {
		return zero, e
	}
	stage, e := s.Files.Stage(ctx, p, "mask-upload-"+in.RequestID, "mask.png", &encoded)
	if e != nil {
		return zero, e
	}
	asset, e := s.Files.Commit(ctx, p, "mask-commit-"+in.RequestID, stage)
	if e != nil {
		return zero, e
	}
	result, e := store.WriteRequest(ctx, s.DB, store.Scope{ProjectID: p, Operation: "generate.mask", RequestID: in.RequestID}, canonical, func(tx *sql.Tx) ([]byte, error) { return json.Marshal(asset) })
	if e == nil {
		e = json.Unmarshal(result, &asset)
	}
	return asset, e
}
