// Package config loads 12-factor configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config is the full application configuration.
type Config struct {
	HTTPAddr         string
	DatabaseURL      string
	DBMaxConns       int32
	DBMinConns       int32
	StatementTimeout time.Duration
	PgBouncer        bool
	RequestTimeout   time.Duration
	ShutdownTimeout  time.Duration
	NodeID           int64
	LogLevel         string

	SeedBooks   int
	SeedAuthors int
	SeedUsers   int
	SeedRandom  uint64
	SeedWorkers int
	SeedBatch   int
}

// Getenv abstracts os.Getenv for tests.
type Getenv func(string) string

// Load reads configuration using getenv (os.Getenv when nil).
func Load(getenv Getenv) (Config, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	l := loader{get: getenv}
	c := Config{
		HTTPAddr:         l.str("HTTP_ADDR", ":8080"),
		DatabaseURL:      l.str("DATABASE_URL", "postgres://library:library@localhost:25432/library?sslmode=disable"),
		DBMaxConns:       int32(l.int("DB_MAX_CONNS", 32)),
		DBMinConns:       int32(l.int("DB_MIN_CONNS", 4)),
		StatementTimeout: l.dur("DB_STATEMENT_TIMEOUT", 5*time.Second),
		PgBouncer:        l.bool("DB_PGBOUNCER", false),
		RequestTimeout:   l.dur("REQUEST_TIMEOUT", 10*time.Second),
		ShutdownTimeout:  l.dur("SHUTDOWN_TIMEOUT", 15*time.Second),
		NodeID:           int64(l.int("NODE_ID", 1)),
		LogLevel:         l.str("LOG_LEVEL", "info"),
		SeedBooks:        l.int("SEED_BOOKS", 1_000_000),
		SeedAuthors:      l.int("SEED_AUTHORS", 200_000),
		SeedUsers:        l.int("SEED_USERS", 100),
		SeedRandom:       uint64(l.int("SEED_RANDOM", 42)),
		SeedWorkers:      l.int("SEED_WORKERS", 8),
		SeedBatch:        l.int("SEED_BATCH", 50_000),
	}
	if l.err != nil {
		return c, l.err
	}
	if c.NodeID < 0 || c.NodeID > 1000 {
		return c, fmt.Errorf("NODE_ID must be in [0, 1000] (1001+ reserved for seed)")
	}
	if c.SeedBooks < 0 || c.SeedAuthors < 1 || c.SeedUsers < 1 || c.SeedWorkers < 1 || c.SeedBatch < 1 {
		return c, fmt.Errorf("SEED_* values must be positive")
	}
	return c, nil
}

type loader struct {
	get Getenv
	err error
}

func (l *loader) str(k, def string) string {
	if v := l.get(k); v != "" {
		return v
	}
	return def
}

func (l *loader) int(k string, def int) int {
	v := l.get(k)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil && l.err == nil {
		l.err = fmt.Errorf("%s: invalid integer %q", k, v)
	}
	return n
}

func (l *loader) bool(k string, def bool) bool {
	v := l.get(k)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil && l.err == nil {
		l.err = fmt.Errorf("%s: invalid boolean %q", k, v)
	}
	return b
}

func (l *loader) dur(k string, def time.Duration) time.Duration {
	v := l.get(k)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil && l.err == nil {
		l.err = fmt.Errorf("%s: invalid duration %q", k, v)
	}
	return d
}
