// Package state persists "job X was sent for slot T" markers on disk so that
// restarts and repeated `once` invocations do not produce duplicate emails.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const slotLayout = "20060102T1504Z"

// Store is a directory-backed state store. Each sent (job, slot) pair is a
// small JSON file: <dir>/sent/<job>/<slot>.json. Writes are atomic
// (temp file + fsync + rename).
type Store struct {
	dir string
}

// Record is the content of a sent marker.
type Record struct {
	Job       string    `json:"job"`
	Slot      time.Time `json:"slot"`
	SentAt    time.Time `json:"sent_at"`
	MessageID string    `json:"message_id"`
}

// Open creates the state directory if needed and checks it is writable.
func Open(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("state directory is not set (STATE_DIR)")
	}
	if err := os.MkdirAll(filepath.Join(dir, "sent"), 0o750); err != nil {
		return nil, fmt.Errorf("create state dir: %w", err)
	}
	probe, err := os.CreateTemp(dir, ".probe-*")
	if err != nil {
		return nil, fmt.Errorf("state dir %q is not writable: %w", dir, err)
	}
	_ = probe.Close()
	_ = os.Remove(probe.Name())
	return &Store{dir: dir}, nil
}

func (s *Store) path(job string, slot time.Time) string {
	return filepath.Join(s.dir, "sent", job, slot.UTC().Format(slotLayout)+".json")
}

// IsSent reports whether job was already sent for slot.
func (s *Store) IsSent(job string, slot time.Time) (bool, error) {
	_, err := os.Stat(s.path(job, slot))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

// MarkSent durably records that job was sent for slot.
func (s *Store) MarkSent(r Record) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return writeAtomic(s.path(r.Job, r.Slot), data)
}

// LastTick returns the last minute the daemon processed (zero if unknown).
func (s *Store) LastTick() (time.Time, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, "last_tick"))
	if errors.Is(err, os.ErrNotExist) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	t, err := time.Parse(time.RFC3339, string(data))
	if err != nil {
		return time.Time{}, fmt.Errorf("corrupt last_tick: %w", err)
	}
	return t, nil
}

// SetLastTick stores the last processed minute.
func (s *Store) SetLastTick(t time.Time) error {
	return writeAtomic(filepath.Join(s.dir, "last_tick"), []byte(t.UTC().Format(time.RFC3339)))
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
