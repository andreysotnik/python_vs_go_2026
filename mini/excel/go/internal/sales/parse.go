package sales

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Excel stores dates as the number of days since 1899-12-30 (taking the
// historical 1900 leap-year bug into account for all serials >= 61).
var excelEpoch = time.Date(1899, time.December, 30, 0, 0, 0, 0, time.UTC)

// Reasonable bounds for an Excel date serial: 1900-03-01 .. 9999-12-31.
const (
	minExcelSerial = 61
	maxExcelSerial = 2958465
)

// trimSpace trims all Unicode white space including non-breaking spaces.
func trimSpace(s string) string {
	return strings.TrimFunc(s, unicode.IsSpace)
}

// removeSpaces drops every white space rune (used for thousand separators
// such as "1 234,50").
func removeSpaces(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

// parseDecimal parses a number that may use either '.' or ',' as decimal
// separator. Only one separator is allowed.
func parseDecimal(s string) (float64, error) {
	s = removeSpaces(s)
	if s == "" {
		return 0, errors.New("empty value")
	}
	if strings.Contains(s, ".") && strings.Contains(s, ",") {
		return 0, fmt.Errorf("ambiguous decimal separators in %q", s)
	}
	v, err := strconv.ParseFloat(strings.Replace(s, ",", ".", 1), 64)
	if err != nil {
		return 0, fmt.Errorf("not a number: %q", s)
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, fmt.Errorf("not a finite number: %q", s)
	}
	return v, nil
}

// ParseDate parses an order date given either as an Excel date serial
// (e.g. "45668") or as text in DD.MM.YYYY format. The time-of-day part of
// a serial is dropped.
func ParseDate(raw string) (time.Time, error) {
	s := trimSpace(raw)
	if s == "" {
		return time.Time{}, errors.New("empty date")
	}
	if serial, err := strconv.ParseFloat(s, 64); err == nil {
		if math.IsNaN(serial) || serial < minExcelSerial || serial > maxExcelSerial {
			return time.Time{}, fmt.Errorf("excel date serial out of range: %q", s)
		}
		return excelEpoch.AddDate(0, 0, int(math.Floor(serial))), nil
	}
	t, err := time.Parse("02.01.2006", s)
	if err != nil {
		return time.Time{}, fmt.Errorf("unparseable date %q (want Excel date or DD.MM.YYYY)", s)
	}
	return t, nil
}

// ParseQuantity parses a strictly positive integer quantity.
func ParseQuantity(raw string) (int, error) {
	s := trimSpace(raw)
	if s == "" {
		return 0, errors.New("empty quantity")
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, fmt.Errorf("quantity is not a number: %q", s)
	}
	if v != math.Trunc(v) {
		return 0, fmt.Errorf("quantity is not an integer: %q", s)
	}
	if v <= 0 {
		return 0, fmt.Errorf("quantity must be > 0, got %q", s)
	}
	if v > math.MaxInt32 {
		return 0, fmt.Errorf("quantity is too large: %q", s)
	}
	return int(v), nil
}

// ParsePrice parses a strictly positive unit price. Both '.' and ',' are
// accepted as decimal separator ("1234,50", "1234.5").
func ParsePrice(raw string) (float64, error) {
	s := trimSpace(raw)
	if s == "" {
		return 0, errors.New("empty unit_price")
	}
	v, err := parseDecimal(s)
	if err != nil {
		return 0, fmt.Errorf("unit_price: %w", err)
	}
	if v <= 0 {
		return 0, fmt.Errorf("unit_price must be > 0, got %q", s)
	}
	return v, nil
}

// ParseDiscount parses a discount given as a fraction in [0, 1] or as a
// percentage text ("15%" -> 0.15). An empty value means no discount.
func ParseDiscount(raw string) (float64, error) {
	s := trimSpace(raw)
	if s == "" {
		return 0, nil
	}
	var (
		v   float64
		err error
	)
	if pct, ok := strings.CutSuffix(s, "%"); ok {
		v, err = parseDecimal(pct)
		v /= 100
	} else {
		v, err = parseDecimal(s)
	}
	if err != nil {
		return 0, fmt.Errorf("discount: %w", err)
	}
	if v < 0 || v > 1 {
		return 0, fmt.Errorf("discount must be within [0, 1], got %q", s)
	}
	return v, nil
}

// NormalizeRegion trims surrounding white space from a region name.
func NormalizeRegion(raw string) string {
	return trimSpace(raw)
}

// ParseAmount parses a non-negative money amount (e.g. a revenue target);
// '.' and ',' are accepted as decimal separator.
func ParseAmount(raw string) (float64, error) {
	v, err := parseDecimal(trimSpace(raw))
	if err != nil {
		return 0, err
	}
	if v < 0 {
		return 0, fmt.Errorf("amount must be >= 0, got %q", raw)
	}
	return v, nil
}
