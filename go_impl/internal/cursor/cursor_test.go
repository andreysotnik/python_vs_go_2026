package cursor

import (
	"encoding/base64"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	cases := []Cursor{
		{Sort: "title", Value: StrPtr("Война и мир 战争与和平"), ID: 42},
		{Sort: "-publication_year", Value: nil, ID: 9007199254740993},
		{Sort: "rank", Value: StrPtr("0.123456789012345"), ID: 1},
	}
	for _, c := range cases {
		s := Encode(c)
		got, err := Decode(s, c.Sort)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.ID != c.ID || got.Sort != c.Sort || (got.Value == nil) != (c.Value == nil) || (c.Value != nil && *got.Value != *c.Value) {
			t.Fatalf("round trip mismatch: %+v vs %+v", got, c)
		}
	}
}

func TestDecodeErrors(t *testing.T) {
	good := Encode(Cursor{Sort: "title", Value: StrPtr("a"), ID: 1})
	bad := []struct{ in, sort string }{
		{"", "title"},
		{"!!!", "title"},
		{base64.RawURLEncoding.EncodeToString([]byte("not json")), "title"},
		{base64.RawURLEncoding.EncodeToString([]byte(`{"s":"title","i":"x"}`)), "title"},
		{base64.RawURLEncoding.EncodeToString([]byte(`{"s":"title","i":"-5"}`)), "title"},
		{good, "-title"},
		{string(make([]byte, MaxLen+1)), "title"},
	}
	for _, b := range bad {
		if _, err := Decode(b.in, b.sort); err == nil {
			t.Fatalf("expected error for %q", b.in)
		}
	}
}
