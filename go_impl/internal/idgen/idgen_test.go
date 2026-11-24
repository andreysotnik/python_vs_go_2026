package idgen

import (
	"testing"
	"time"
)

func TestNextMonotonicUnique(t *testing.T) {
	g, err := New(7)
	if err != nil {
		t.Fatal(err)
	}
	fixed := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	g.now = func() time.Time { return fixed }
	seen := map[int64]bool{}
	var prev int64
	for i := 0; i < 10000; i++ {
		id := g.Next()
		if seen[id] || id <= prev {
			t.Fatalf("non-monotonic or duplicate id %d after %d", id, prev)
		}
		seen[id] = true
		prev = id
	}
}

func TestClockBackwards(t *testing.T) {
	g, _ := New(1)
	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	g.now = func() time.Time { return t0 }
	a := g.Next()
	g.now = func() time.Time { return t0.Add(-time.Second) }
	b := g.Next()
	if b <= a {
		t.Fatalf("expected monotonic ids, got %d then %d", a, b)
	}
}

func TestBadNode(t *testing.T) {
	if _, err := New(-1); err == nil {
		t.Fatal("expected error")
	}
	if _, err := New(1024); err == nil {
		t.Fatal("expected error")
	}
}

func TestTimeRoundTrip(t *testing.T) {
	ts := time.Date(2024, 5, 6, 7, 8, 9, 123000000, time.UTC)
	id := FromTime(ts, 3, 5)
	if !Time(id).Equal(ts) {
		t.Fatalf("got %v want %v", Time(id), ts)
	}
	if id <= 0 {
		t.Fatal("id must be positive")
	}
}
