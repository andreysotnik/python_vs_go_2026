package integration

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/example/worldlib/internal/config"
	"github.com/example/worldlib/internal/dbx"
	"github.com/example/worldlib/internal/explain"
	"github.com/example/worldlib/internal/seed"
)

// TestSeedAndExplain loads a small dataset into a dedicated database, checks counts, that all
// indexes / foreign keys were restored after the bulk load, and that the EXPLAIN tool runs.
func TestSeedAndExplain(t *testing.T) {
	ctx := context.Background()
	const name = "library_seed_it"
	u, err := createDB(ctx, adminDB, name)
	if err != nil {
		t.Fatal(err)
	}
	defer dropDB(ctx, adminDB, name)
	cfg, _ := config.Load(func(k string) string {
		if k == "DATABASE_URL" {
			return u
		}
		return ""
	})
	cfg.DBMaxConns = 8
	p, err := dbx.NewPool(ctx, cfg, "seed-test")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	countObjs := func() (idx, fks int) {
		_ = p.QueryRow(ctx, `SELECT count(*) FROM pg_index i JOIN pg_class t ON t.oid = i.indrelid WHERE t.relname IN ('books','authors','audit_log','book_authors','author_books','book_isbns')`).Scan(&idx)
		_ = p.QueryRow(ctx, `SELECT count(*) FROM pg_constraint WHERE contype = 'f' AND conparentid = 0`).Scan(&fks)
		return
	}
	idx0, fk0 := countObjs()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	opt := seed.Options{Books: 3000, Authors: 400, Users: 5, Random: 7, Workers: 3, Batch: 700}
	for run := 0; run < 2; run++ { // second run proves idempotency (truncate + reload)
		st, err := seed.Run(ctx, p, log, opt)
		if err != nil {
			t.Fatal(err)
		}
		if st.Rows["books"] != 3000 || st.Rows["authors"] != 400 || st.Rows["users"] != 5 || st.Rows["audit_log"] != 3400 {
			t.Fatalf("unexpected counts %v", st.Rows)
		}
		if st.Rows["book_authors"] != st.Rows["author_books"] || st.Rows["book_authors"] < 3000 {
			t.Fatalf("link counts %v", st.Rows)
		}
	}
	idx1, fk1 := countObjs()
	if idx0 != idx1 || fk0 != fk1 {
		t.Fatalf("indexes/fks not restored: %d/%d -> %d/%d", idx0, fk0, idx1, fk1)
	}
	var invalid, books int
	_ = p.QueryRow(ctx, "SELECT count(*) FROM pg_index WHERE NOT indisvalid").Scan(&invalid)
	_ = p.QueryRow(ctx, "SELECT count(*) FROM books b JOIN book_isbns i ON i.book_id = b.id AND i.isbn_13 = b.isbn_13").Scan(&books)
	if invalid != 0 || books == 0 {
		t.Fatalf("invalid=%d isbn-joined=%d", invalid, books)
	}
	var cyr, cjk, ar int
	_ = p.QueryRow(ctx, `SELECT count(*) FILTER (WHERE title ~ '[а-яА-Я]'), count(*) FILTER (WHERE title ~ '[一-龥]'), count(*) FILTER (WHERE title ~ '[ء-ي]') FROM books`).Scan(&cyr, &cjk, &ar)
	if cyr == 0 || cjk == 0 || ar == 0 {
		t.Fatalf("multilingual titles expected: %d %d %d", cyr, cjk, ar)
	}

	res, err := explain.Run(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	explain.Report(&buf, res, "test")
	if len(res) < 30 || !strings.Contains(buf.String(), "Execution Time") {
		t.Fatalf("explain report incomplete: %d cases", len(res))
	}
	for _, r := range res {
		if r.Offset {
			t.Fatalf("OFFSET in %s", r.Name)
		}
	}
}
