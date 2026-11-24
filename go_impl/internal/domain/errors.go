// Package domain holds entities, inputs, validation rules and domain errors.
package domain

import (
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors mapped to HTTP statuses by the transport layer.
var (
	ErrNotFound             = errors.New("not found")
	ErrVersionConflict      = errors.New("version conflict")
	ErrDuplicate            = errors.New("duplicate")
	ErrPreconditionRequired = errors.New("precondition required")
	ErrUnauthenticated      = errors.New("unauthenticated")
)

// FieldError describes a single invalid field.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// ValidationError is a list of field errors. Kind distinguishes malformed requests (400)
// from semantically invalid entities (422).
type ValidationError struct {
	Kind   ValidationKind
	Errors []FieldError
}

// ValidationKind classifies validation errors.
type ValidationKind int

// Validation kinds.
const (
	KindUnprocessable ValidationKind = iota // 422: body is well-formed but violates rules
	KindBadRequest                          // 400: malformed query parameters / cursor
)

func (e *ValidationError) Error() string {
	parts := make([]string, 0, len(e.Errors))
	for _, fe := range e.Errors {
		parts = append(parts, fmt.Sprintf("%s: %s", fe.Field, fe.Message))
	}
	return "validation failed: " + strings.Join(parts, "; ")
}

// Add appends a field error.
func (e *ValidationError) Add(field, msg string) { e.Errors = append(e.Errors, FieldError{field, msg}) }

// OrNil returns nil if there are no errors.
func (e *ValidationError) OrNil() error {
	if e == nil || len(e.Errors) == 0 {
		return nil
	}
	return e
}

// BadRequest builds a 400 validation error for a single parameter.
func BadRequest(field, msg string) error {
	return &ValidationError{Kind: KindBadRequest, Errors: []FieldError{{field, msg}}}
}

// Unprocessable builds a 422 validation error for a single field.
func Unprocessable(field, msg string) error {
	return &ValidationError{Kind: KindUnprocessable, Errors: []FieldError{{field, msg}}}
}
