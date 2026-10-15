package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-playground/validator/v10"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
)

type Scope = store.Scope

const maxJSONBody = 1 << 20

var validation = validator.New(validator.WithRequiredStructEnabled())

// Validate applies explicit validate tags on typed request DTOs.
func Validate(value any) error { return validation.Struct(value) }

// DecodeJSON accepts one typed object. DTOs must declare nested object types,
// rather than maps, so DisallowUnknownFields also protects nested mutations.
func DecodeJSON[T any](w http.ResponseWriter, r *http.Request) (T, error) {
	var result T
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxJSONBody))
	if err != nil {
		return result, err
	}
	if _, err = CanonicalJSON(raw); err != nil {
		return result, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	dec.UseNumber()
	if err = dec.Decode(&result); err != nil {
		return result, err
	}
	err = Validate(result)
	return result, err
}

func CanonicalJSON(raw []byte) ([]byte, error) { return store.CanonicalJSON(raw) }
