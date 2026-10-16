package server

import (
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/providers"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"io"
	"net/http"
)

func serveMask(w http.ResponseWriter, r *http.Request, s *providers.Service, p string) {
	raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, maxJSONBody))
	if e != nil {
		reject(w, 400, "invalid_input", "Invalid mask body")
		return
	}
	in, e := store.DecodeValue[providers.MaskInput](raw)
	if e != nil {
		reject(w, 400, "invalid_input", "Invalid mask schema")
		return
	}
	in.OriginalWire = raw
	asset, e := s.CreateMask(r.Context(), p, in)
	if e != nil {
		generationError(w, e)
		return
	}
	respond(w, 200, map[string]any{"asset_id": asset.ID, "asset": asset})
}
