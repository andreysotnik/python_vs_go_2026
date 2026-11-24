// Package idgen generates snowflake-like 63-bit identifiers:
// 41 bits of milliseconds since Epoch | 10 bits node id | 12 bits sequence.
// IDs are roughly time-ordered, which keeps B-tree inserts append-mostly.
package idgen

import (
	"errors"
	"sync"
	"time"
)

// Epoch is the custom epoch (2020-01-01T00:00:00Z).
var Epoch = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

const (
	nodeBits = 10
	seqBits  = 12
	maxNode  = 1<<nodeBits - 1
	maxSeq   = 1<<seqBits - 1
)

// Node ids reserved for bulk loading (seed) so they never collide with online nodes.
const (
	SeedNodeAuthors = 1001
	SeedNodeBooks   = 1002
	SeedNodeAudit   = 1003
	SeedNodeUsers   = 1004
)

// Generator is a goroutine-safe snowflake generator.
type Generator struct {
	mu     sync.Mutex
	node   int64
	lastMs int64
	seq    int64
	now    func() time.Time
}

// New returns a generator for the given node id (0..1023).
func New(node int64) (*Generator, error) {
	if node < 0 || node > maxNode {
		return nil, errors.New("idgen: node id must be in [0, 1023]")
	}
	return &Generator{node: node, now: time.Now}, nil
}

// Next returns the next id.
func (g *Generator) Next() int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	ms := g.now().Sub(Epoch).Milliseconds()
	if ms < g.lastMs { // clock went backwards: stay on last timestamp
		ms = g.lastMs
	}
	if ms == g.lastMs {
		g.seq++
		if g.seq > maxSeq {
			ms++
			g.seq = 0
		}
	} else {
		g.seq = 0
	}
	g.lastMs = ms
	return Compose(ms, g.node, g.seq)
}

// Compose builds an id from parts.
func Compose(ms, node, seq int64) int64 {
	return ms<<(nodeBits+seqBits) | (node&maxNode)<<seqBits | (seq & maxSeq)
}

// FromTime builds an id for a given timestamp (used by the seeder).
func FromTime(t time.Time, node, seq int64) int64 {
	return Compose(t.Sub(Epoch).Milliseconds(), node, seq)
}

// Time extracts the timestamp from an id.
func Time(id int64) time.Time {
	return Epoch.Add(time.Duration(id>>(nodeBits+seqBits)) * time.Millisecond)
}
