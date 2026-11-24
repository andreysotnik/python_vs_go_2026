package numfmt

import "testing"

func TestThousands(t *testing.T) {
	tests := []struct {
		v    float64
		dec  int
		want string
	}{
		{0, 0, "0"},
		{999, 0, "999"},
		{1000, 0, "1 000"},
		{1234567.891, 2, "1 234 567,89"},
		{207149040.5, 2, "207 149 040,50"},
		{-1234.5, 1, "-1 234,5"},
		{-0.001, 2, "0,00"},
		{12.5, 1, "12,5"},
	}
	for _, tc := range tests {
		if got := Thousands(tc.v, tc.dec); got != tc.want {
			t.Errorf("Thousands(%v, %d) = %q, want %q", tc.v, tc.dec, got, tc.want)
		}
	}
}

func TestMoneyAndCompact(t *testing.T) {
	if got := Money(1234567.5); got != "1 234 567,50 ₽" {
		t.Errorf("Money = %q", got)
	}
	tests := map[float64]string{
		17481031.5: "17,5 млн",
		8001840:    "8 млн",
		2.5e9:      "2,5 млрд",
		1500:       "1,5 тыс",
		12:         "12",
	}
	for v, want := range tests {
		if got := Compact(v); got != want {
			t.Errorf("Compact(%v) = %q, want %q", v, got, want)
		}
	}
}
