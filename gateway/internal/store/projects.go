package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

type Objects struct{ DB *sql.DB }

// DomainError carries the public error contract across HTTP and future MCP callers.
type DomainError struct {
	Status        int
	Code, Message string
}

func (e *DomainError) Error() string { return e.Message }
func invalid(message string) error   { return &DomainError{400, "invalid_input", message} }
func missing() error                 { return &DomainError{404, "not_found", "Object not found in this project"} }
func protected() error {
	return &DomainError{409, "invalid_input", "Object is referenced; remove dependent objects first"}
}

func domainError(err error) error {
	if err == nil {
		return nil
	}
	var d *DomainError
	if errors.As(err, &d) || errors.Is(err, ErrRequestConflict) || errors.Is(err, ErrRequestPending) {
		return err
	}
	if errors.Is(err, sql.ErrNoRows) {
		return missing()
	}
	return &DomainError{502, "upstream", "Database operation failed"}
}

type Project struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"created_at"`
}
type projectValue struct {
	Name string `json:"name" validate:"required,max=256"`
}

func (s Objects) CreateProject(ctx context.Context, name, requestID string) (Project, error) {
	value, _ := json.Marshal(projectValue{Name: name})
	raw, err := s.Mutate(ctx, "_app", "projects", Mutation{RequestID: requestID, Action: "create", Value: value})
	var p Project
	if err == nil {
		err = json.Unmarshal(raw, &p)
	}
	return p, err
}

func createProject(ctx context.Context, tx *sql.Tx, value json.RawMessage) (json.RawMessage, error) {
	v, err := DecodeValue[projectValue](value)
	if err != nil {
		return nil, invalid(err.Error())
	}
	p := Project{ID: uuid.NewString(), Name: v.Name, CreatedAt: time.Now().UnixMilli()}
	if _, err = tx.ExecContext(ctx, `INSERT INTO projects(id,name,created_at) VALUES(?,?,?)`, p.ID, p.Name, p.CreatedAt); err != nil {
		return nil, err
	}
	return json.Marshal(p)
}
