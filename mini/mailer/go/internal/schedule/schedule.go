// Package schedule wraps 5-field cron expressions bound to a time zone.
package schedule

import (
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
)

var parser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// Schedule is a parsed cron expression evaluated in a specific location.
type Schedule struct {
	expr  string
	loc   *time.Location
	sched cron.Schedule
}

// Parse parses a standard 5-field cron expression and a IANA time zone name.
// An empty time zone means UTC.
func Parse(expr, tz string) (*Schedule, error) {
	loc, err := LoadLocation(tz)
	if err != nil {
		return nil, err
	}
	s, err := parser.Parse(expr)
	if err != nil {
		return nil, fmt.Errorf("invalid cron expression %q: %w", expr, err)
	}
	return &Schedule{expr: expr, loc: loc, sched: s}, nil
}

// LoadLocation resolves a time zone name; empty means UTC.
func LoadLocation(tz string) (*time.Location, error) {
	if tz == "" {
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, fmt.Errorf("unknown timezone %q: %w", tz, err)
	}
	return loc, nil
}

// Location returns the schedule's time zone.
func (s *Schedule) Location() *time.Location { return s.loc }

// String returns the source expression.
func (s *Schedule) String() string { return s.expr }

// Slot truncates t to the start of its minute.
func Slot(t time.Time) time.Time { return t.Truncate(time.Minute) }

// Matches reports whether the schedule fires in the minute containing t.
func (s *Schedule) Matches(t time.Time) bool {
	slot := Slot(t).In(s.loc)
	return s.sched.Next(slot.Add(-time.Second)).Equal(slot)
}

// Next returns the first firing minute strictly after t.
func (s *Schedule) Next(t time.Time) time.Time {
	return s.sched.Next(t.In(s.loc))
}
