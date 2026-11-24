// Package cursor implements opaque keyset-pagination cursors.
//
// A cursor is base64url(JSON) of the sort key, the last sort value (nil means the
// row had a NULL sort value, i.e. pagination is in the "NULLS LAST" phase) and the
// last id used as tie-breaker. Cursors are bound to the sort they were issued for.
package cursor

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
)

// ErrInvalid is returned for undecodable or mismatched cursors.
var ErrInvalid = errors.New("invalid cursor")

// MaxLen bounds the encoded cursor size accepted from clients.
const MaxLen = 4096

// Cursor is the decoded keyset position.
type Cursor struct {
	Sort  string  `json:"s"`
	Value *string `json:"v"`
	ID    int64   `json:"i"`
}

type wire struct {
	Sort  string  `json:"s"`
	Value *string `json:"v"`
	ID    string  `json:"i"`
}

// Encode returns the opaque string.
func Encode(c Cursor) string {
	b, _ := json.Marshal(wire{Sort: c.Sort, Value: c.Value, ID: strconv.FormatInt(c.ID, 10)})
	return base64.RawURLEncoding.EncodeToString(b)
}

// Decode parses an opaque cursor and checks it was issued for wantSort.
func Decode(s, wantSort string) (Cursor, error) {
	if s == "" || len(s) > MaxLen {
		return Cursor{}, ErrInvalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, ErrInvalid
	}
	var w wire
	if err := json.Unmarshal(raw, &w); err != nil {
		return Cursor{}, ErrInvalid
	}
	id, err := strconv.ParseInt(w.ID, 10, 64)
	if err != nil || id <= 0 {
		return Cursor{}, ErrInvalid
	}
	if w.Sort != wantSort {
		return Cursor{}, ErrInvalid
	}
	return Cursor{Sort: w.Sort, Value: w.Value, ID: id}, nil
}

// StrPtr is a helper for building cursor values.
func StrPtr(s string) *string { return &s }
