package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"

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
	if err = exactJSONFields(raw, reflect.TypeFor[T]()); err != nil {
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

// encoding/json matches fields without regard to case. Explicit request DTOs
// require exact field names, including inside pointers and array elements.
func exactJSONFields(raw json.RawMessage, typ reflect.Type) error {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ == reflect.TypeFor[json.RawMessage]() {
		return nil
	} // Its caller must decode the subdocument with its own DTO.
	switch typ.Kind() {
	case reflect.Struct:
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil {
			return err
		}
		fields := map[string]reflect.Type{}
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			if !field.IsExported() {
				continue
			}
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name == "-" {
				continue
			}
			if name == "" {
				name = field.Name
			}
			fields[name] = field.Type
		}
		for name, value := range object {
			field, ok := fields[name]
			if !ok {
				return fmt.Errorf("unknown field %q", name)
			}
			if err := exactJSONFields(value, field); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		// []byte uses JSON's base64 string representation, not an array DTO.
		if typ.Elem().Kind() == reflect.Uint8 {
			return nil
		}
		var values []json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return err
		}
		for _, value := range values {
			if err := exactJSONFields(value, typ.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}
