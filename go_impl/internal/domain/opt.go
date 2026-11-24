package domain

import (
	"bytes"
	"encoding/json"
)

// Opt is a tri-state JSON field: absent (Set=false), explicit null (Set && Null), or a value.
// It gives PATCH (JSON merge patch) semantics without map[string]any juggling.
type Opt[T any] struct {
	Set   bool
	Null  bool
	Value T
}

// UnmarshalJSON implements json.Unmarshaler.
func (o *Opt[T]) UnmarshalJSON(b []byte) error {
	o.Set = true
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		o.Null = true
		var zero T
		o.Value = zero
		return nil
	}
	o.Null = false
	return json.Unmarshal(b, &o.Value)
}

// Some builds a set, non-null Opt.
func Some[T any](v T) Opt[T] { return Opt[T]{Set: true, Value: v} }

// applyPtr applies an Opt to a pointer field.
func applyPtr[T any](dst **T, o Opt[T]) {
	if !o.Set {
		return
	}
	if o.Null {
		*dst = nil
		return
	}
	v := o.Value
	*dst = &v
}
