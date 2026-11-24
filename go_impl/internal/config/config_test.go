package config

import (
	"testing"
	"time"
)

func env(m map[string]string) Getenv { return func(k string) string { return m[k] } }

func TestDefaults(t *testing.T) {
	c, err := Load(env(nil))
	if err != nil || c.HTTPAddr != ":8080" || c.SeedBooks != 1_000_000 || c.SeedAuthors != 200_000 || c.StatementTimeout != 5*time.Second || c.DBMaxConns != 32 {
		t.Fatalf("%v %+v", err, c)
	}
}

func TestOverridesAndErrors(t *testing.T) {
	c, err := Load(env(map[string]string{"HTTP_ADDR": ":9", "DB_PGBOUNCER": "true", "DB_STATEMENT_TIMEOUT": "250ms", "SEED_BOOKS": "10", "NODE_ID": "7"}))
	if err != nil || c.HTTPAddr != ":9" || !c.PgBouncer || c.StatementTimeout != 250*time.Millisecond || c.SeedBooks != 10 || c.NodeID != 7 {
		t.Fatalf("%v %+v", err, c)
	}
	for _, bad := range []map[string]string{
		{"SEED_BOOKS": "x"}, {"DB_PGBOUNCER": "maybe"}, {"REQUEST_TIMEOUT": "10"}, {"NODE_ID": "1001"}, {"SEED_AUTHORS": "0"},
	} {
		if _, err := Load(env(bad)); err == nil {
			t.Fatalf("%v accepted", bad)
		}
	}
	if _, err := Load(nil); err != nil {
		t.Fatal(err)
	}
}
