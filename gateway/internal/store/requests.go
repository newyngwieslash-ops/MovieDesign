package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strconv"
	"unicode/utf8"

	"github.com/go-playground/validator/v10"
)

// Scope identifies one user intent within a project and operation.
type Scope struct {
	ProjectID string `validate:"required,max=128"`
	Operation string `validate:"required,max=128"`
	RequestID string `validate:"required,max=128"`
}

var validate = validator.New(validator.WithRequiredStructEnabled())
var identifier = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
var ErrRequestConflict = errors.New("requestId already used with different input")
var ErrRequestPending = errors.New("request has no completed receipt")

func (s Scope) Validate() error {
	if err := validate.Struct(s); err != nil {
		return err
	}
	if !identifier.MatchString(s.ProjectID) || !identifier.MatchString(s.RequestID) {
		return errors.New("invalid project_id or requestId")
	}
	return nil
}

// WriteRequest commits the mutation and its receipt together. Input must be the
// complete JSON from the request boundary. Never use this for file I/O.
func WriteRequest(ctx context.Context, db *sql.DB, scope Scope, input []byte, mutate func(*sql.Tx) ([]byte, error)) ([]byte, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	canonical, err := CanonicalJSON(input)
	if err != nil {
		return nil, err
	}
	input = canonical
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var previous, state string
	var response sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT input_json,state,response_json FROM write_requests WHERE project_id=? AND operation=? AND request_id=?`, scope.ProjectID, scope.Operation, scope.RequestID).Scan(&previous, &state, &response)
	if err == nil {
		if previous != string(input) {
			return nil, ErrRequestConflict
		}
		if state != "completed" || !response.Valid {
			return nil, ErrRequestPending
		}
		return []byte(response.String), nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO write_requests(project_id,operation,request_id,input_json,state) VALUES(?,?,?,?,'pending')`, scope.ProjectID, scope.Operation, scope.RequestID, string(input))
	if err != nil {
		return nil, err
	}
	result, err := mutate(tx)
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE write_requests SET state='completed',response_json=? WHERE project_id=? AND operation=? AND request_id=?`, string(result), scope.ProjectID, scope.Operation, scope.RequestID)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

// CanonicalJSON sorts object keys and preserves array order, number tokens and
// string contents. Duplicate keys are rejected before a struct or map sees them.
func CanonicalJSON(raw []byte) ([]byte, error) {
	if !utf8.Valid(raw) {
		return nil, errors.New("invalid UTF-8")
	}
	if err := validSurrogateEscapes(raw); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	token, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if token != json.Delim('{') {
		return nil, errors.New("expected JSON object")
	}
	if err = scanObject(dec, 0); err != nil {
		return nil, err
	}
	if _, err = dec.Token(); err != io.EOF {
		return nil, errors.New("expected one JSON object")
	}
	dec = json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var value any
	if err = dec.Decode(&value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}
func scanObject(dec *json.Decoder, depth int) error {
	seen := map[string]bool{}
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return err
		}
		name, ok := key.(string)
		if !ok {
			return errors.New("expected object key")
		}
		if seen[name] {
			return errors.New("duplicate JSON key")
		}
		seen[name] = true
		if err = scanValue(dec, depth+1); err != nil {
			return err
		}
	}
	_, err := dec.Token()
	return err
}
func scanValue(dec *json.Decoder, depth int) error {
	if depth > 128 {
		return errors.New("JSON nesting exceeds limit")
	}
	token, err := dec.Token()
	if err != nil {
		return err
	}
	if token == json.Delim('{') {
		return scanObject(dec, depth)
	}
	if token == json.Delim('[') {
		for dec.More() {
			if err = scanValue(dec, depth+1); err != nil {
				return err
			}
		}
		_, err = dec.Token()
		return err
	}
	return nil
}

// The standard decoder replaces unpaired UTF-16 escapes with U+FFFD, which
// would collapse distinct prompts into the same idempotency input.
func validSurrogateEscapes(raw []byte) error {
	for i := 0; i+1 < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		if raw[i+1] != 'u' {
			i++
			continue
		}
		if i+6 > len(raw) {
			return errors.New("invalid Unicode escape")
		}
		unit, err := strconv.ParseUint(string(raw[i+2:i+6]), 16, 16)
		if err != nil {
			return err
		}
		if unit >= 0xdc00 && unit <= 0xdfff {
			return errors.New("unpaired low surrogate")
		}
		if unit >= 0xd800 && unit <= 0xdbff {
			if i+12 > len(raw) || raw[i+6] != '\\' || raw[i+7] != 'u' {
				return errors.New("unpaired high surrogate")
			}
			low, err := strconv.ParseUint(string(raw[i+8:i+12]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return errors.New("unpaired high surrogate")
			}
			i += 6
		}
		i += 5
	}
	return nil
}
