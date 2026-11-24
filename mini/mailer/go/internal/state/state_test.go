package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMarkAndCheck(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	slot := time.Date(2026, 11, 19, 9, 0, 0, 0, time.FixedZone("MSK", 3*3600))
	if ok, err := s.IsSent("job", slot); err != nil || ok {
		t.Fatalf("IsSent before mark = %v, %v", ok, err)
	}
	if err := s.MarkSent(Record{Job: "job", Slot: slot, SentAt: time.Now(), MessageID: "<x@y>"}); err != nil {
		t.Fatal(err)
	}
	// Same instant expressed in UTC must be recognised as the same slot.
	if ok, err := s.IsSent("job", slot.UTC()); err != nil || !ok {
		t.Fatalf("IsSent after mark = %v, %v", ok, err)
	}
	if ok, _ := s.IsSent("other", slot); ok {
		t.Fatal("other job must not be marked")
	}
	if ok, _ := s.IsSent("job", slot.Add(time.Minute)); ok {
		t.Fatal("other slot must not be marked")
	}
	// Survives "restart".
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := s2.IsSent("job", slot); !ok {
		t.Fatal("state lost after reopen")
	}
	// No temp files left behind.
	files, _ := filepath.Glob(filepath.Join(dir, "sent", "job", ".tmp-*"))
	if len(files) != 0 {
		t.Fatalf("temp files left: %v", files)
	}
}

func TestLastTick(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if lt, err := s.LastTick(); err != nil || !lt.IsZero() {
		t.Fatalf("initial LastTick = %v, %v", lt, err)
	}
	now := time.Date(2026, 11, 19, 6, 0, 0, 0, time.UTC)
	if err := s.SetLastTick(now); err != nil {
		t.Fatal(err)
	}
	if lt, err := s.LastTick(); err != nil || !lt.Equal(now) {
		t.Fatalf("LastTick = %v, %v", lt, err)
	}
}

func TestOpenErrors(t *testing.T) {
	if _, err := Open(""); err == nil {
		t.Fatal("expected error for empty dir")
	}
	f := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(f, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(f); err == nil {
		t.Fatal("expected error when STATE_DIR is a file")
	}
}
