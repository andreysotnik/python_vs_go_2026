// Package numfmt formats numbers for human readers (Russian conventions:
// space as thousands separator, comma as decimal separator).
package numfmt

import (
	"math"
	"strconv"
	"strings"
)

// Thousands formats v with the given number of decimals, grouping integer
// digits by three with a space: 1234567.891 -> "1 234 567,89".
func Thousands(v float64, decimals int) string {
	s := strconv.FormatFloat(math.Abs(v), 'f', decimals, 64)
	intPart, frac, _ := strings.Cut(s, ".")

	var b strings.Builder
	if v < 0 && strings.Trim(s, "0.") != "" {
		b.WriteByte('-')
	}
	for i, d := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteRune(' ')
		}
		b.WriteRune(d)
	}
	if frac != "" {
		b.WriteByte(',')
		b.WriteString(frac)
	}
	return b.String()
}

// Money formats an amount in rubles with kopecks: "1 234 567,50 ₽".
func Money(v float64) string {
	return Thousands(v, 2) + " ₽"
}

// Compact formats large amounts for axis labels: 12 500 000 -> "12,5 млн".
func Compact(v float64) string {
	a := math.Abs(v)
	switch {
	case a >= 1e9:
		return trimZeros(Thousands(v/1e9, 1)) + " млрд"
	case a >= 1e6:
		return trimZeros(Thousands(v/1e6, 1)) + " млн"
	case a >= 1e3:
		return trimZeros(Thousands(v/1e3, 1)) + " тыс"
	default:
		return trimZeros(Thousands(v, 1))
	}
}

func trimZeros(s string) string {
	return strings.TrimSuffix(s, ",0")
}
