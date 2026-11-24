package domain

import (
	"strings"
	"time"
)

// DateLayout is the ISO-8601 calendar date format.
const DateLayout = "2006-01-02"

// Date is a calendar date serialized as YYYY-MM-DD.
type Date struct{ time.Time }

// MarshalJSON implements json.Marshaler.
func (d Date) MarshalJSON() ([]byte, error) {
	return []byte(`"` + d.Format(DateLayout) + `"`), nil
}

// UnmarshalJSON implements json.Unmarshaler.
func (d *Date) UnmarshalJSON(b []byte) error {
	t, err := time.Parse(DateLayout, strings.Trim(string(b), `"`))
	if err != nil {
		return err
	}
	d.Time = t
	return nil
}

// String returns YYYY-MM-DD.
func (d Date) String() string { return d.Format(DateLayout) }
