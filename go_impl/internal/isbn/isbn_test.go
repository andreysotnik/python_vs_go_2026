package isbn

import "testing"

func TestValid13(t *testing.T) {
	cases := map[string]bool{
		"9780306406157": true,
		"9780306406158": false,
		"9790306406156": true,
		"978030640615":  false,
		"1230306406157": false,
		"97803064061X7": false,
	}
	for in, want := range cases {
		if got := Valid13(in); got != want {
			t.Errorf("Valid13(%q)=%v want %v", in, got, want)
		}
	}
}

func TestValid10AndConvert(t *testing.T) {
	if !Valid10("0306406152") {
		t.Fatal("0306406152 must be valid")
	}
	if !Valid10("080442957X") {
		t.Fatal("080442957X must be valid")
	}
	if Valid10("0306406153") || Valid10("03064061") || Valid10("03064A6152") {
		t.Fatal("invalid isbn10 accepted")
	}
	got, err := To13("0306406152")
	if err != nil || got != "9780306406157" {
		t.Fatalf("To13 got %q %v", got, err)
	}
	back, err := To10(got)
	if err != nil || back != "0306406152" {
		t.Fatalf("To10 got %q %v", back, err)
	}
	if _, err := To13("bad"); err == nil {
		t.Fatal("expected error")
	}
	if _, err := To10("9790000000001"); err == nil {
		t.Fatal("979 cannot convert to isbn10")
	}
}

func TestNormalizeCanonical(t *testing.T) {
	if Normalize("978-0-306-40615-7") != "9780306406157" {
		t.Fatal("normalize failed")
	}
	if Normalize("0-8044-2957-x") != "080442957X" {
		t.Fatal("normalize X failed")
	}
	for _, in := range []string{"978-0-306-40615-7", "0-306-40615-2", "0306406152"} {
		got, err := ToCanonical13(in)
		if err != nil || got != "9780306406157" {
			t.Fatalf("ToCanonical13(%q)=%q,%v", in, got, err)
		}
	}
	for _, in := range []string{"", "abc", "978030640615", "9780306406158"} {
		if _, err := ToCanonical13(in); err == nil {
			t.Fatalf("ToCanonical13(%q) must fail", in)
		}
	}
}

func TestCheckDigitErrors(t *testing.T) {
	if _, err := CheckDigit13("12"); err == nil {
		t.Fatal("expected error")
	}
	if _, err := CheckDigit13("12345678901a"); err == nil {
		t.Fatal("expected error")
	}
	if _, err := CheckDigit10("1"); err == nil {
		t.Fatal("expected error")
	}
	if _, err := CheckDigit10("12345678a"); err == nil {
		t.Fatal("expected error")
	}
}

func TestGenerate13(t *testing.T) {
	for _, body := range []uint64{0, 1, 306406157 / 10, 999999999, 123456789} {
		for _, p := range []string{"978", "979"} {
			s := Generate13(p, body)
			if !Valid13(s) {
				t.Fatalf("generated invalid isbn %s", s)
			}
		}
	}
	if Generate13("978", 30640615) != "9780306406157" {
		t.Fatalf("got %s", Generate13("978", 30640615))
	}
}
