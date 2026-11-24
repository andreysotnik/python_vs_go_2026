// Package isbn validates, normalizes and generates ISBN-10/ISBN-13 codes.
package isbn

import (
	"errors"
	"strings"
)

// ErrInvalid is returned for malformed ISBNs or wrong check digits.
var ErrInvalid = errors.New("invalid ISBN")

// Normalize strips hyphens and spaces and upper-cases the trailing X.
func Normalize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == 'x' || r == 'X':
			b.WriteRune('X')
		case r == '-' || r == ' ':
		default:
			b.WriteRune(r) // keep garbage so validation fails
		}
	}
	return b.String()
}

// CheckDigit13 computes the ISBN-13 check digit for a 12-digit prefix.
func CheckDigit13(prefix12 string) (byte, error) {
	if len(prefix12) != 12 {
		return 0, ErrInvalid
	}
	sum := 0
	for i := 0; i < 12; i++ {
		c := prefix12[i]
		if c < '0' || c > '9' {
			return 0, ErrInvalid
		}
		d := int(c - '0')
		if i%2 == 1 {
			d *= 3
		}
		sum += d
	}
	return byte('0' + (10-sum%10)%10), nil
}

// CheckDigit10 computes the ISBN-10 check digit for a 9-digit prefix.
func CheckDigit10(prefix9 string) (byte, error) {
	if len(prefix9) != 9 {
		return 0, ErrInvalid
	}
	sum := 0
	for i := 0; i < 9; i++ {
		c := prefix9[i]
		if c < '0' || c > '9' {
			return 0, ErrInvalid
		}
		sum += int(c-'0') * (10 - i)
	}
	r := (11 - sum%11) % 11
	if r == 10 {
		return 'X', nil
	}
	return byte('0' + r), nil
}

// Valid13 reports whether s (normalized) is a valid ISBN-13 (978/979 prefix).
func Valid13(s string) bool {
	if len(s) != 13 || (!strings.HasPrefix(s, "978") && !strings.HasPrefix(s, "979")) {
		return false
	}
	cd, err := CheckDigit13(s[:12])
	return err == nil && cd == s[12]
}

// Valid10 reports whether s (normalized) is a valid ISBN-10.
func Valid10(s string) bool {
	if len(s) != 10 {
		return false
	}
	cd, err := CheckDigit10(s[:9])
	return err == nil && cd == s[9]
}

// To13 converts a valid ISBN-10 to ISBN-13.
func To13(isbn10 string) (string, error) {
	if !Valid10(isbn10) {
		return "", ErrInvalid
	}
	p := "978" + isbn10[:9]
	cd, _ := CheckDigit13(p)
	return p + string(cd), nil
}

// To10 converts a 978-prefixed ISBN-13 to ISBN-10.
func To10(isbn13 string) (string, error) {
	if !Valid13(isbn13) || !strings.HasPrefix(isbn13, "978") {
		return "", ErrInvalid
	}
	p := isbn13[3:12]
	cd, _ := CheckDigit10(p)
	return p + string(cd), nil
}

// ToCanonical13 accepts an ISBN-10 or ISBN-13 in any formatting and returns ISBN-13.
func ToCanonical13(s string) (string, error) {
	n := Normalize(s)
	switch len(n) {
	case 13:
		if Valid13(n) {
			return n, nil
		}
	case 10:
		return To13(n)
	}
	return "", ErrInvalid
}

// Generate13 builds a valid ISBN-13 from a prefix ("978"/"979") and a 9-digit body number.
func Generate13(prefix string, body uint64) string {
	var buf [12]byte
	copy(buf[:3], prefix)
	for i := 11; i >= 3; i-- {
		buf[i] = byte('0' + body%10)
		body /= 10
	}
	cd, _ := CheckDigit13(string(buf[:]))
	return string(buf[:]) + string(cd)
}
