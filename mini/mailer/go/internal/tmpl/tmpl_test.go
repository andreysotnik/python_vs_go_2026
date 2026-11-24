package tmpl

import (
	"testing"
	"time"
)

func TestRender(t *testing.T) {
	msk, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	// 22:30 UTC on Nov 18 is already Nov 19 in Moscow.
	slot := time.Date(2026, 11, 18, 22, 30, 0, 0, time.UTC)
	got := Render("Отчёт {{job}} за {{date}} ({{datetime}}) {{ date }} {{unknown}}", "daily", slot, msk)
	want := "Отчёт daily за 2026-11-19 (2026-11-19 01:30 +03:00) 2026-11-19 {{unknown}}"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestRenderNilLocationIsUTC(t *testing.T) {
	slot := time.Date(2026, 11, 18, 22, 30, 0, 0, time.UTC)
	if got := Render("{{date}}", "j", slot, nil); got != "2026-11-18" {
		t.Fatalf("got %q", got)
	}
}

func TestRenderNoPlaceholders(t *testing.T) {
	if got := Render("plain {text}", "j", time.Now(), time.UTC); got != "plain {text}" {
		t.Fatalf("got %q", got)
	}
}
