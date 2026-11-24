package seed

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"

	"github.com/example/worldlib/internal/idgen"
)

// Options controls the seeding run.
type Options struct {
	Books   int
	Authors int
	Users   int
	Random  uint64
	Workers int
	Batch   int
}

// Stats summarizes a run.
type Stats struct {
	Rows     map[string]int64
	Load     time.Duration
	Indexes  time.Duration
	Analyze  time.Duration
	Total    time.Duration
	RowsPerS float64
}

// Loaded tables (parents). Secondary indexes and FKs on them are dropped during COPY.
var loadTables = []string{"authors", "books", "book_authors", "author_books", "book_isbns", "audit_log"}

type loader struct {
	pool *pgxpool.Pool
	log  *slog.Logger
	opt  Options
	gen  *Gen
	cnt  map[string]*atomic.Int64
}

// Run truncates catalog tables and loads a fresh deterministic dataset.
func Run(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger, opt Options) (Stats, error) {
	l := &loader{pool: pool, log: log, opt: opt, gen: NewGen(opt.Random, opt.Authors, opt.Books, opt.Users), cnt: map[string]*atomic.Int64{}}
	for _, t := range append([]string{"users"}, loadTables...) {
		l.cnt[t] = &atomic.Int64{}
	}
	st := Stats{Rows: map[string]int64{}}
	start := time.Now()

	if err := l.prepare(ctx); err != nil {
		return st, fmt.Errorf("prepare: %w", err)
	}
	stop := l.progress(ctx)
	t0 := time.Now()
	err := l.loadAll(ctx)
	stop()
	if err != nil {
		return st, fmt.Errorf("load: %w", err)
	}
	st.Load = time.Since(t0)
	var total int64
	for k, c := range l.cnt {
		st.Rows[k] = c.Load()
		total += c.Load()
	}
	st.RowsPerS = float64(total) / st.Load.Seconds()
	log.Info("copy finished", "seconds", st.Load.Seconds(), "rows", total, "rows_per_s", int64(st.RowsPerS))

	t1 := time.Now()
	if err := l.restoreDDL(ctx); err != nil {
		return st, fmt.Errorf("rebuild indexes: %w", err)
	}
	st.Indexes = time.Since(t1)
	t2 := time.Now()
	if err := l.analyze(ctx); err != nil {
		return st, fmt.Errorf("analyze: %w", err)
	}
	st.Analyze = time.Since(t2)
	st.Total = time.Since(start)
	return st, nil
}

func (l *loader) prepare(ctx context.Context) error {
	if _, err := l.pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS seed_ddl_backup (kind text NOT NULL, name text NOT NULL, tbl text NOT NULL, ddl text NOT NULL)`); err != nil {
		return err
	}
	// Restore anything left over from a crashed run before taking a new snapshot.
	if err := l.restoreDDL(ctx); err != nil {
		return err
	}
	l.log.Info("truncating catalog tables")
	if _, err := l.pool.Exec(ctx, `TRUNCATE audit_log, book_isbns, author_books, book_authors, books, authors`); err != nil {
		return err
	}
	if _, err := l.pool.Exec(ctx, `DELETE FROM users WHERE id <> 1`); err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, l.pool, func(tx pgx.Tx) error {
		// Foreign keys first (they may depend on nothing we drop, but must go before COPY).
		if _, err := tx.Exec(ctx, `INSERT INTO seed_ddl_backup (kind, name, tbl, ddl)
SELECT 'fk', c.conname, c.conrelid::regclass::text, pg_get_constraintdef(c.oid)
FROM pg_constraint c
WHERE c.contype = 'f' AND c.conparentid = 0 AND c.conrelid::regclass::text = ANY($1)`, loadTables); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO seed_ddl_backup (kind, name, tbl, ddl)
SELECT 'index', ci.relname, t.relname, pg_get_indexdef(i.indexrelid)
FROM pg_index i JOIN pg_class ci ON ci.oid = i.indexrelid JOIN pg_class t ON t.oid = i.indrelid
WHERE t.relname = ANY($1) AND NOT i.indisprimary AND NOT i.indisunique`, loadTables); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT kind, name, tbl FROM seed_ddl_backup`)
		if err != nil {
			return err
		}
		type item struct{ kind, name, tbl string }
		items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (item, error) {
			var it item
			return it, r.Scan(&it.kind, &it.name, &it.tbl)
		})
		if err != nil {
			return err
		}
		for _, it := range items {
			var q string
			if it.kind == "fk" {
				q = fmt.Sprintf("ALTER TABLE %s DROP CONSTRAINT %s", pgx.Identifier{it.tbl}.Sanitize(), pgx.Identifier{it.name}.Sanitize())
			} else {
				q = "DROP INDEX " + pgx.Identifier{it.name}.Sanitize()
			}
			if _, err := tx.Exec(ctx, q); err != nil {
				return fmt.Errorf("%s: %w", q, err)
			}
		}
		l.log.Info("dropped secondary indexes and foreign keys for bulk load", "objects", len(items))
		return nil
	})
}

// restoreDDL recreates indexes (in parallel) and then foreign keys from the backup table.
func (l *loader) restoreDDL(ctx context.Context) error {
	rows, err := l.pool.Query(ctx, `SELECT kind, name, tbl, ddl FROM seed_ddl_backup ORDER BY kind DESC, tbl, name`)
	if err != nil {
		return err
	}
	type item struct{ kind, name, tbl, ddl string }
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (item, error) {
		var it item
		return it, r.Scan(&it.kind, &it.name, &it.tbl, &it.ddl)
	})
	if err != nil || len(items) == 0 {
		return err
	}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(max(1, l.opt.Workers/2))
	var fks []item
	for _, it := range items {
		if it.kind == "fk" {
			fks = append(fks, it)
			continue
		}
		g.Go(func() error {
			t := time.Now()
			conn, err := l.pool.Acquire(gctx)
			if err != nil {
				return err
			}
			defer conn.Release()
			if _, err := conn.Exec(gctx, "SET maintenance_work_mem = '512MB'"); err != nil {
				return err
			}
			// pg_get_indexdef() renders "ON ONLY parent" for partitioned tables, which would create an
			// invalid parent index without partition indexes; build it recursively instead.
			ddl := strings.Replace(it.ddl, " ON ONLY ", " ON ", 1)
			if _, err := conn.Exec(gctx, ddl); err != nil {
				return fmt.Errorf("%s: %w", it.name, err)
			}
			if _, err := conn.Exec(gctx, "DELETE FROM seed_ddl_backup WHERE kind = 'index' AND name = $1", it.name); err != nil {
				return err
			}
			l.log.Info("index built", "index", it.name, "table", it.tbl, "seconds", time.Since(t).Seconds())
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}
	for _, fk := range fks {
		t := time.Now()
		q := fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s %s", pgx.Identifier{fk.tbl}.Sanitize(), pgx.Identifier{fk.name}.Sanitize(), fk.ddl)
		if _, err := l.pool.Exec(ctx, q); err != nil {
			return fmt.Errorf("%s: %w", fk.name, err)
		}
		if _, err := l.pool.Exec(ctx, "DELETE FROM seed_ddl_backup WHERE kind = 'fk' AND name = $1 AND tbl = $2", fk.name, fk.tbl); err != nil {
			return err
		}
		l.log.Info("foreign key validated", "constraint", fk.name, "table", fk.tbl, "seconds", time.Since(t).Seconds())
	}
	var invalid int
	if err := l.pool.QueryRow(ctx, "SELECT count(*) FROM pg_index WHERE NOT indisvalid").Scan(&invalid); err != nil {
		return err
	}
	if invalid > 0 {
		return fmt.Errorf("%d invalid indexes after rebuild", invalid)
	}
	return nil
}

func (l *loader) analyze(ctx context.Context) error {
	g, gctx := errgroup.WithContext(ctx)
	for _, t := range append([]string{"users"}, loadTables...) {
		g.Go(func() error {
			_, err := l.pool.Exec(gctx, "VACUUM (ANALYZE) "+pgx.Identifier{t}.Sanitize())
			return err
		})
	}
	return g.Wait()
}

func (l *loader) progress(ctx context.Context) func() {
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		start := time.Now()
		tick := time.NewTicker(3 * time.Second)
		defer tick.Stop()
		var last int64
		lastT := start
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-tick.C:
				var total int64
				attrs := []any{}
				for _, t := range loadTables {
					v := l.cnt[t].Load()
					total += v
					attrs = append(attrs, t, v)
				}
				rate := float64(total-last) / now.Sub(lastT).Seconds()
				avg := float64(total) / now.Sub(start).Seconds()
				attrs = append(attrs, "rows_total", total, "rows_per_s", int64(rate), "avg_rows_per_s", int64(avg),
					"books_pct", fmt.Sprintf("%.1f", 100*float64(l.cnt["books"].Load())/float64(max(1, l.opt.Books))))
				l.log.Info("seed progress", attrs...)
				last, lastT = total, now
			}
		}
	}()
	return func() { cancel(); wg.Wait() }
}

type chunk struct{ from, to int }

func chunks(n, size int) []chunk {
	var out []chunk
	for s := 0; s < n; s += size {
		out = append(out, chunk{s, min(n, s+size)})
	}
	return out
}

func (l *loader) copyRows(ctx context.Context, conn *pgxpool.Conn, table string, cols []string, rows [][]any) error {
	n, err := conn.CopyFrom(ctx, pgx.Identifier{table}, cols, pgx.CopyFromRows(rows))
	if err != nil {
		return fmt.Errorf("copy %s: %w", table, err)
	}
	l.cnt[table].Add(n)
	return nil
}

func (l *loader) loadAll(ctx context.Context) error {
	// users
	urows := make([][]any, 0, len(l.gen.UserIDs))
	for k, id := range l.gen.UserIDs {
		urows = append(urows, []any{id, fmt.Sprintf("librarian_%04d", k+1), fmt.Sprintf("librarian%04d@library.local", k+1),
			windowStart.Add(-time.Duration(len(l.gen.UserIDs)-k) * time.Hour)})
	}
	conn, err := l.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	err = l.copyRows(ctx, conn, "users", []string{"id", "username", "email", "created_at"}, urows)
	conn.Release()
	if err != nil {
		return err
	}

	type job struct {
		kind string
		c    chunk
	}
	jobs := make(chan job)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		defer close(jobs)
		for _, c := range chunks(l.opt.Authors, l.opt.Batch) {
			select {
			case jobs <- job{"authors", c}:
			case <-gctx.Done():
				return gctx.Err()
			}
		}
		for _, c := range chunks(l.opt.Books, l.opt.Batch) {
			select {
			case jobs <- job{"books", c}:
			case <-gctx.Done():
				return gctx.Err()
			}
		}
		return nil
	})
	for w := 0; w < l.opt.Workers; w++ {
		g.Go(func() error {
			conn, err := l.pool.Acquire(gctx)
			if err != nil {
				return err
			}
			defer conn.Release()
			for j := range jobs {
				var err error
				if j.kind == "authors" {
					err = l.authorChunk(gctx, conn, j.c)
				} else {
					err = l.bookChunk(gctx, conn, j.c)
				}
				if err != nil {
					return err
				}
			}
			return nil
		})
	}
	return g.Wait()
}

var auditCols = []string{"id", "entity_type", "entity_id", "action", "changed_by", "changed_at", "diff"}

func (l *loader) authorChunk(ctx context.Context, conn *pgxpool.Conn, c chunk) error {
	rows := make([][]any, 0, c.to-c.from)
	audit := make([][]any, 0, c.to-c.from)
	for j := c.from; j < c.to; j++ {
		a := l.gen.Author(j)
		rows = append(rows, []any{a.ID, a.FullName, a.OriginalName, a.BirthDate, a.DeathDate, a.CountryCode, a.Biography,
			a.ExternalIDs, a.CreatedAt, a.CreatedBy, a.CreatedAt, a.CreatedBy})
		diff, _ := json.Marshal(map[string]any{"full_name": map[string]any{"old": nil, "new": a.FullName}, "source": "seed"})
		audit = append(audit, []any{idgen.FromTime(a.CreatedAt, idgen.SeedNodeAudit, int64(j%4096)), "author", a.ID, "create",
			a.CreatedBy, a.CreatedAt, diff})
	}
	if err := l.copyRows(ctx, conn, "authors", []string{"id", "full_name", "original_name", "birth_date", "death_date",
		"country_code", "biography", "external_ids", "created_at", "created_by", "updated_at", "updated_by"}, rows); err != nil {
		return err
	}
	return l.copyRows(ctx, conn, "audit_log", auditCols, audit)
}

func (l *loader) bookChunk(ctx context.Context, conn *pgxpool.Conn, c chunk) error {
	n := c.to - c.from
	books := make([][]any, 0, n)
	links := make([][]any, 0, n*2)
	rev := make([][]any, 0, n*2)
	isbns := make([][]any, 0, n)
	audit := make([][]any, 0, n)
	for i := c.from; i < c.to; i++ {
		b := l.gen.Book(i)
		books = append(books, []any{b.ID, b.Title, b.OriginalTitle, b.Subtitle, b.Description, b.ISBN10, b.ISBN13,
			b.LanguageCode, b.PublicationYear, b.Publisher, b.PageCount, b.Genres, b.Edition, b.ExternalIDs,
			b.CreatedAt, b.CreatedBy, b.CreatedAt, b.CreatedBy})
		for _, lk := range b.Links {
			links = append(links, []any{b.ID, lk.AuthorID, lk.Role, lk.Ordinal})
			rev = append(rev, []any{lk.AuthorID, b.ID, lk.Role, lk.Ordinal})
		}
		if b.ISBN13 != nil {
			isbns = append(isbns, []any{*b.ISBN13, b.ID})
		}
		diff, _ := json.Marshal(map[string]any{"title": map[string]any{"old": nil, "new": b.Title}, "source": "seed"})
		audit = append(audit, []any{idgen.FromTime(b.CreatedAt, seedNodeAuditBooks, int64(i%4096)), "book", b.ID, "create",
			b.CreatedBy, b.CreatedAt, diff})
	}
	if err := l.copyRows(ctx, conn, "books", []string{"id", "title", "original_title", "subtitle", "description", "isbn_10",
		"isbn_13", "language_code", "publication_year", "publisher", "page_count", "genres", "edition", "external_ids",
		"created_at", "created_by", "updated_at", "updated_by"}, books); err != nil {
		return err
	}
	lc := []string{"book_id", "author_id", "role", "ordinal"}
	if err := l.copyRows(ctx, conn, "book_authors", lc, links); err != nil {
		return err
	}
	if err := l.copyRows(ctx, conn, "author_books", []string{"author_id", "book_id", "role", "ordinal"}, rev); err != nil {
		return err
	}
	if err := l.copyRows(ctx, conn, "book_isbns", []string{"isbn_13", "book_id"}, isbns); err != nil {
		return err
	}
	return l.copyRows(ctx, conn, "audit_log", auditCols, audit)
}
