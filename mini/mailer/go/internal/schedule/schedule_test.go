package schedule

import (
	"testing"
	"time"
)

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestMatchesWithTimezone(t *testing.T) {
	s, err := Parse("0 9 * * 1-5", "Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		at   string
		want bool
	}{
		{"2026-11-19T09:00:00+03:00", true},  // Thursday 09:00 MSK
		{"2026-11-19T06:00:00Z", true},       // same instant in UTC
		{"2026-11-19T09:00:59+03:00", true},  // any second within the minute
		{"2026-11-19T09:01:00+03:00", false}, // next minute
		{"2026-11-19T09:00:00Z", false},      // 09:00 UTC is 12:00 MSK
		{"2026-11-21T09:00:00+03:00", false}, // Saturday
		{"2026-11-23T09:00:00+03:00", true},  // Monday
	}
	for _, c := range cases {
		if got := s.Matches(mustTime(t, c.at)); got != c.want {
			t.Errorf("Matches(%s) = %v, want %v", c.at, got, c.want)
		}
	}
}

func TestEmptyTimezoneIsUTC(t *testing.T) {
	s, err := Parse("30 6 * * *", "")
	if err != nil {
		t.Fatal(err)
	}
	if s.Location() != time.UTC {
		t.Fatalf("location = %v", s.Location())
	}
	if !s.Matches(mustTime(t, "2026-01-01T06:30:00Z")) {
		t.Fatal("expected match at 06:30 UTC")
	}
}

func TestDSTTransition(t *testing.T) {
	// New York springs forward on 2026-03-08: 02:00 -> 03:00.
	s, err := Parse("30 2 * * *", "America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	// 2:30 local doesn't exist that day; cron must not fire at a bogus instant
	// and must fire the next day at 02:30 EDT (06:30Z).
	next := s.Next(mustTime(t, "2026-03-08T00:00:00-05:00"))
	if next.Day() == 8 && next.Hour() == 2 {
		t.Fatalf("fired at non-existent local time: %v", next)
	}
	if !s.Matches(mustTime(t, "2026-03-09T06:30:00Z")) {
		t.Fatal("expected match at 02:30 EDT on 2026-03-09")
	}
	// Winter: 02:30 EST = 07:30Z.
	if !s.Matches(mustTime(t, "2026-01-15T07:30:00Z")) {
		t.Fatal("expected match at 02:30 EST")
	}
}

func TestStepsListsAndRanges(t *testing.T) {
	s, err := Parse("*/15 8-10 1,15 * *", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	if !s.Matches(mustTime(t, "2026-05-15T10:45:00Z")) {
		t.Fatal("expected match")
	}
	if s.Matches(mustTime(t, "2026-05-15T10:44:00Z")) || s.Matches(mustTime(t, "2026-05-16T10:45:00Z")) {
		t.Fatal("unexpected match")
	}
}

func TestParseErrors(t *testing.T) {
	bad := []struct{ expr, tz string }{
		{"0 9 * *", "UTC"},     // 4 fields
		{"0 9 * * * *", "UTC"}, // 6 fields (seconds) not allowed
		{"61 9 * * *", "UTC"},  // minute out of range
		{"0 25 * * *", "UTC"},  // hour out of range
		{"0 9 * * 1-5", "Mars/Olympus"},
		{"@every 5m", "UTC"},
	}
	for _, b := range bad {
		if _, err := Parse(b.expr, b.tz); err == nil {
			t.Errorf("Parse(%q,%q) expected error", b.expr, b.tz)
		}
	}
}

func TestNext(t *testing.T) {
	s, _ := Parse("0 9 * * 1-5", "Europe/Moscow")
	got := s.Next(mustTime(t, "2026-11-20T09:00:00+03:00")) // Friday at fire time
	want := mustTime(t, "2026-11-23T09:00:00+03:00")        // Monday
	if !got.Equal(want) {
		t.Fatalf("Next = %v, want %v", got, want)
	}
}
