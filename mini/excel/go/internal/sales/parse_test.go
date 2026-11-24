package sales

import (
	"testing"
	"time"
)

func day(m time.Month, d int) time.Time {
	return time.Date(2025, m, d, 0, 0, 0, 0, time.UTC)
}

// Rule 3: order_date is an Excel date or DD.MM.YYYY text.
func TestParseDate(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want time.Time
		ok   bool
	}{
		{"excel serial", "45668", day(1, 11), true},
		{"excel serial 2025-01-01", "45658", day(1, 1), true},
		{"excel serial with time", "45658.75", day(1, 1), true},
		{"excel serial padded", " 45658 ", day(1, 1), true},
		{"dd.mm.yyyy", "14.11.2025", day(11, 14), true},
		{"dd.mm.yyyy with spaces", "  01.02.2025 ", day(2, 1), true},
		{"text", "когда-то", time.Time{}, false},
		{"empty", "", time.Time{}, false},
		{"impossible day", "31.02.2025", time.Time{}, false},
		{"iso format not allowed", "2025-01-11", time.Time{}, false},
		{"single digit day", "1.2.2025", time.Time{}, false},
		{"negative serial", "-5", time.Time{}, false},
		{"zero serial", "0", time.Time{}, false},
		{"huge serial", "1e9", time.Time{}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseDate(tc.in)
			if tc.ok != (err == nil) {
				t.Fatalf("ParseDate(%q) error = %v, want ok=%v", tc.in, err, tc.ok)
			}
			if tc.ok && !got.Equal(tc.want) {
				t.Errorf("ParseDate(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// Rule 4: quantity is an integer > 0.
func TestParseQuantity(t *testing.T) {
	tests := []struct {
		in   string
		want int
		ok   bool
	}{
		{"1", 1, true},
		{"10", 10, true},
		{" 3 ", 3, true},
		{"2.0", 2, true},
		{"", 0, false},
		{"   ", 0, false},
		{"n/a", 0, false},
		{"два", 0, false},
		{"0", 0, false},
		{"-1", 0, false},
		{"-10", 0, false},
		{"2.5", 0, false},
		{"NaN", 0, false},
		{"Inf", 0, false},
	}
	for _, tc := range tests {
		got, err := ParseQuantity(tc.in)
		if tc.ok != (err == nil) {
			t.Errorf("ParseQuantity(%q) error = %v, want ok=%v", tc.in, err, tc.ok)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseQuantity(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// Rule 5: unit_price is a number > 0, '.' or ',' as decimal separator.
func TestParsePrice(t *testing.T) {
	tests := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"6490", 6490, true},
		{"1234,50", 1234.5, true},
		{"1234.5", 1234.5, true},
		{"1 234,50", 1234.5, true},
		{"0.01", 0.01, true},
		{"", 0, false},
		{"договорная", 0, false},
		{"0", 0, false},
		{"-100", 0, false},
		{"1.234,50", 0, false},
		{"12,3,4", 0, false},
	}
	for _, tc := range tests {
		got, err := ParsePrice(tc.in)
		if tc.ok != (err == nil) {
			t.Errorf("ParsePrice(%q) error = %v, want ok=%v", tc.in, err, tc.ok)
			continue
		}
		if got != tc.want {
			t.Errorf("ParsePrice(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// Rule 6: discount is a fraction in [0, 1] or "N%"; empty means 0.
func TestParseDiscount(t *testing.T) {
	tests := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"", 0, true},
		{"  ", 0, true},
		{"0", 0, true},
		{"0.15", 0.15, true},
		{"0,15", 0.15, true},
		{"1", 1, true},
		{"15%", 0.15, true},
		{"0%", 0, true},
		{" 20 % ", 0.2, true},
		{"12,5%", 0.125, true},
		{"100%", 1, true},
		{"-0.1", 0, false},
		{"1.5", 0, false},
		{"150%", 0, false},
		{"скидка", 0, false},
		{"%", 0, false},
	}
	for _, tc := range tests {
		got, err := ParseDiscount(tc.in)
		if tc.ok != (err == nil) {
			t.Errorf("ParseDiscount(%q) error = %v, want ok=%v", tc.in, err, tc.ok)
			continue
		}
		if diff := got - tc.want; diff > 1e-12 || diff < -1e-12 {
			t.Errorf("ParseDiscount(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// Rule 7: region is trimmed.
func TestNormalizeRegion(t *testing.T) {
	tests := map[string]string{
		"  Москва ":         "Москва",
		"Казань":            "Казань",
		"\tНовосибирск\n":   "Новосибирск",
		" Екатеринбург":     "Екатеринбург",
		"Санкт-Петербург  ": "Санкт-Петербург",
		"   ":               "",
	}
	for in, want := range tests {
		if got := NormalizeRegion(in); got != want {
			t.Errorf("NormalizeRegion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseAmount(t *testing.T) {
	if v, err := ParseAmount("3350000"); err != nil || v != 3350000 {
		t.Errorf("ParseAmount = %v, %v", v, err)
	}
	if v, err := ParseAmount("1000,5"); err != nil || v != 1000.5 {
		t.Errorf("ParseAmount comma = %v, %v", v, err)
	}
	for _, bad := range []string{"", "abc", "-1"} {
		if _, err := ParseAmount(bad); err == nil {
			t.Errorf("ParseAmount(%q) expected error", bad)
		}
	}
}
