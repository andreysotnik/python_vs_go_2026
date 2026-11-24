// Package explain runs EXPLAIN (ANALYZE, BUFFERS) for the key API queries on a seeded database,
// fails if any of them performs a Seq Scan on a large table or uses OFFSET, and renders a
// Markdown report (docs/QUERY_PLANS.md).
package explain

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/example/worldlib/internal/cursor"
	"github.com/example/worldlib/internal/domain"
	"github.com/example/worldlib/internal/repository"
)

// bigTable matches large tables and their partitions.
var bigTable = regexp.MustCompile(`^(books|authors|book_authors|author_books|book_isbns|audit_log)(_p\d+|_\d{4}_\d{2}|_default)?$`)

var seqScanRe = regexp.MustCompile(`Seq Scan on (\w+)`)
var execTimeRe = regexp.MustCompile(`Execution Time: ([\d.]+) ms`)
var offsetRe = regexp.MustCompile(`(?i)\bOFFSET\b`)

// Case is one query under test.
type Case struct {
	Name string
	SQL  string
	Args []any
}

// Result of one case.
type Result struct {
	Case
	Plan     string
	ExecMS   float64
	SeqScans []string
	Offset   bool
}

// OK reports whether the plan is acceptable.
func (r Result) OK() bool { return len(r.SeqScans) == 0 && !r.Offset }

type samples struct {
	authorID    int64
	topAuthorID int64
	bookID      int64
	userID      int64
	isbn        string
	titleWord   string
	nameWord    string
	title       string
	fullName    string
	publisher   string
	createdAt   time.Time
	// ids of a real 100-row page and of its authors (batch lookups must not Seq Scan either)
	pageIDs       []int64
	pageAuthorIDs []int64
}

func loadSamples(ctx context.Context, pool *pgxpool.Pool) (samples, error) {
	var s samples
	err := pool.QueryRow(ctx, `SELECT b.id, b.title, b.created_at, b.publisher FROM books b
WHERE b.deleted_at IS NULL AND b.publisher IS NOT NULL AND b.language_code = 'en' ORDER BY b.created_at, b.id LIMIT 1`).Scan(&s.bookID, &s.title, &s.createdAt, &s.publisher)
	if err != nil {
		return s, fmt.Errorf("no seeded books: %w", err)
	}
	if err := pool.QueryRow(ctx, `SELECT a.id, a.full_name FROM authors a WHERE a.deleted_at IS NULL ORDER BY a.created_at, a.id LIMIT 1`).Scan(&s.authorID, &s.fullName); err != nil {
		return s, err
	}
	// The most prolific author (worst case for "books of author"); sampled from one partition.
	if err := pool.QueryRow(ctx, `SELECT author_id FROM author_books_p00 GROUP BY author_id ORDER BY count(*) DESC LIMIT 1`).Scan(&s.topAuthorID); err != nil {
		return s, err
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE id <> 1 ORDER BY id LIMIT 1`).Scan(&s.userID); err != nil {
		s.userID = 1
	}
	if err := pool.QueryRow(ctx, `SELECT isbn_13 FROM book_isbns WHERE book_id = $1`, s.bookID).Scan(&s.isbn); err != nil {
		_ = pool.QueryRow(ctx, `SELECT isbn_13 FROM book_isbns_p00 LIMIT 1`).Scan(&s.isbn)
	}
	for _, w := range strings.Fields(s.title) {
		if len([]rune(w)) >= 4 {
			s.titleWord = strings.ToLower(w)
			break
		}
	}
	if s.titleWord == "" {
		s.titleWord = "shadow"
	}
	s.nameWord = strings.Fields(s.fullName)[len(strings.Fields(s.fullName))-1]
	rows, err := pool.Query(ctx, `SELECT id FROM books b WHERE b.deleted_at IS NULL ORDER BY b.created_at DESC, b.id DESC LIMIT 100`)
	if err != nil {
		return s, err
	}
	if s.pageIDs, err = pgx.CollectRows(rows, pgx.RowTo[int64]); err != nil {
		return s, err
	}
	rows, err = pool.Query(ctx, repository.BookLinksSQL, s.pageIDs)
	if err != nil {
		return s, err
	}
	for rows.Next() {
		var b, a int64
		var role string
		var ord int16
		if err := rows.Scan(&b, &a, &role, &ord); err != nil {
			rows.Close()
			return s, err
		}
		s.pageAuthorIDs = append(s.pageAuthorIDs, a)
	}
	rows.Close()
	return s, rows.Err()
}

func listParams(sort string, limit int, cur *cursor.Cursor) domain.ListParams {
	return domain.ListParams{Sort: domain.SortKey{Field: strings.TrimLeft(sort, "-"), Desc: strings.HasPrefix(sort, "-")}, Limit: limit, Cursor: cur}
}

func cur(sort string, v *string, id int64) *cursor.Cursor {
	return &cursor.Cursor{Sort: sort, Value: v, ID: id}
}

// Cases builds the query set from the real repository builders.
func Cases(ctx context.Context, pool *pgxpool.Pool) ([]Case, error) {
	s, err := loadSamples(ctx, pool)
	if err != nil {
		return nil, err
	}
	var out []Case
	sp := func(v string) *string { return &v }
	ip := func(v int) *int { return &v }
	lang, ru := "en", "ru"
	genre := "fantasy"
	uid := domain.ID(s.userID)
	aid := domain.ID(s.topAuthorID)
	from := s.createdAt.AddDate(0, -6, 0)
	to := s.createdAt.AddDate(0, 6, 0)
	createdCur := repository.FormatSortValue(s.createdAt)

	addPlan := func(name string, plan repository.KeysetPlan, err error) {
		if err != nil {
			out = append(out, Case{Name: name + " (build error: " + err.Error() + ")"})
			return
		}
		qs, as, _ := plan.SQL()
		for i := range qs {
			n := name
			if len(qs) > 1 {
				n = fmt.Sprintf("%s — phase %d/%d", name, i+1, len(qs))
			}
			out = append(out, Case{Name: n, SQL: qs[i], Args: as[i]})
		}
	}
	books := func(name string, f domain.BookFilter, lp domain.ListParams) {
		p, err := repository.BuildListBooks(f, lp)
		addPlan("GET /books "+name, p, err)
	}
	authors := func(name string, f domain.AuthorFilter, lp domain.ListParams) {
		p, err := repository.BuildListAuthors(f, lp)
		addPlan("GET /authors "+name, p, err)
	}

	books("(default sort -created_at, first page)", domain.BookFilter{}, listParams("-created_at", 20, nil))
	books("sort=-created_at with cursor (deep page)", domain.BookFilter{}, listParams("-created_at", 20, cur("-created_at", createdCur, s.bookID)))
	books("sort=title with cursor", domain.BookFilter{}, listParams("title", 20, cur("title", &s.title, s.bookID)))
	books("sort=-title first page", domain.BookFilter{}, listParams("-title", 100, nil))
	books("sort=publication_year with cursor", domain.BookFilter{}, listParams("publication_year", 20, cur("publication_year", sp("1990"), s.bookID)))
	books("sort=-publication_year (NULLS LAST, two phases)", domain.BookFilter{}, listParams("-publication_year", 20, nil))
	books("sort=publication_year cursor inside NULL tail", domain.BookFilter{}, listParams("publication_year", 20, cur("publication_year", nil, s.bookID)))
	books("language=ru sort=title", domain.BookFilter{Language: &ru}, listParams("title", 20, nil))
	books("language=en year_from=1990 year_to=2000 sort=publication_year", domain.BookFilter{Language: &lang, YearFrom: ip(1990), YearTo: ip(2000)}, listParams("publication_year", 20, nil))
	books("year_from=1800 year_to=1810", domain.BookFilter{YearFrom: ip(1800), YearTo: ip(1810)}, listParams("-created_at", 20, nil))
	books("publisher=…", domain.BookFilter{Publisher: &s.publisher}, listParams("title", 20, nil))
	books("genre=fantasy", domain.BookFilter{Genre: &genre}, listParams("-created_at", 20, nil))
	books("genre=fantasy language=ru sort=title", domain.BookFilter{Genre: &genre, Language: &ru}, listParams("title", 20, nil))
	books("author_id=<most prolific author>", domain.BookFilter{AuthorID: &aid}, listParams("-created_at", 20, nil))
	books("author=<name substring>", domain.BookFilter{Author: &s.nameWord}, listParams("title", 20, nil))
	books("isbn=…", domain.BookFilter{ISBN13: &s.isbn}, listParams("-created_at", 20, nil))
	books("title=<fuzzy substring>", domain.BookFilter{Title: &s.titleWord}, listParams("title", 20, nil))
	books("q=<full-text>", domain.BookFilter{Q: &s.titleWord}, listParams("-created_at", 20, nil))
	books("created_by + created_from/created_to", domain.BookFilter{CreatedBy: &uid, CreatedFrom: &from, CreatedTo: &to}, listParams("-created_at", 20, nil))

	authors("(default sort -created_at)", domain.AuthorFilter{}, listParams("-created_at", 20, nil))
	authors("sort=full_name with cursor", domain.AuthorFilter{}, listParams("full_name", 20, cur("full_name", &s.fullName, s.authorID)))
	authors("sort=-birth_date (NULLS LAST, two phases)", domain.AuthorFilter{}, listParams("-birth_date", 20, nil))
	country := "RU"
	authors("country=RU sort=full_name", domain.AuthorFilter{Country: &country}, listParams("full_name", 20, nil))
	bf, bt := domain.Date{Time: time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)}, domain.Date{Time: time.Date(1901, 1, 1, 0, 0, 0, 0, time.UTC)}
	authors("born_from/born_to sort=birth_date", domain.AuthorFilter{BornFrom: &bf, BornTo: &bt}, listParams("birth_date", 20, nil))
	authors("name=<fuzzy>", domain.AuthorFilter{Name: &s.nameWord}, listParams("full_name", 20, nil))
	authors("q=<full-text>", domain.AuthorFilter{Q: &s.nameWord}, listParams("-created_at", 20, nil))

	ab := repository.BuildAuthorBooks(aid, nil, 20)
	q, a, _ := ab.ToSql()
	out = append(out, Case{Name: "GET /authors/{id}/books (most prolific author)", SQL: q, Args: a})
	ab = repository.BuildAuthorBooks(aid, cur("book_id", sp("author"), s.bookID), 20)
	q, a, _ = ab.ToSql()
	out = append(out, Case{Name: "GET /authors/{id}/books with cursor", SQL: q, Args: a})

	h, _ := repository.BuildHistory("book", domain.ID(s.bookID), nil, 20)
	q, a, _ = h.ToSql()
	out = append(out, Case{Name: "GET /books/{id}/history", SQL: q, Args: a})

	for _, t := range []string{"books", "authors", "all"} {
		q, a, _ := repository.BuildSearch(domain.SearchParams{Q: s.titleWord, Type: t, Limit: 20})
		if t == "authors" {
			q, a, _ = repository.BuildSearch(domain.SearchParams{Q: s.nameWord, Type: t, Limit: 20})
		}
		out = append(out, Case{Name: "GET /search type=" + t, SQL: q, Args: a})
	}
	out = append(out, Case{Name: "GET /books/{id}", SQL: "SELECT b.* FROM books b WHERE b.id = $1 AND b.deleted_at IS NULL", Args: []any{s.bookID}},
		Case{Name: "GET /books page: authors links for 100 books (batch)", SQL: repository.BookLinksSQL, Args: []any{s.pageIDs}},
		Case{Name: "GET /books page: author names by ids (batch)", SQL: repository.AuthorsByIDsSQL, Args: []any{s.pageAuthorIDs}})
	return out, nil
}

// Run executes all cases.
func Run(ctx context.Context, pool *pgxpool.Pool) ([]Result, error) {
	cases, err := Cases(ctx, pool)
	if err != nil {
		return nil, err
	}
	res := make([]Result, 0, len(cases))
	for _, c := range cases {
		r := Result{Case: c, Offset: offsetRe.MatchString(c.SQL)}
		if c.SQL == "" {
			r.SeqScans = []string{"<query could not be built>"}
			res = append(res, r)
			continue
		}
		// Warm-up run so the report reflects steady-state (cached) behaviour.
		if rows, err := pool.Query(ctx, c.SQL, c.Args...); err == nil {
			rows.Close()
		}
		rows, err := pool.Query(ctx, "EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT) "+c.SQL, c.Args...)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", c.Name, err)
		}
		var lines []string
		for rows.Next() {
			var l string
			if err := rows.Scan(&l); err != nil {
				rows.Close()
				return nil, err
			}
			lines = append(lines, l)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("%s: %w", c.Name, err)
		}
		r.Plan = strings.Join(lines, "\n")
		for _, m := range seqScanRe.FindAllStringSubmatch(r.Plan, -1) {
			if bigTable.MatchString(m[1]) {
				r.SeqScans = append(r.SeqScans, m[1])
			}
		}
		if m := execTimeRe.FindStringSubmatch(r.Plan); m != nil {
			_, _ = fmt.Sscanf(m[1], "%f", &r.ExecMS)
		}
		res = append(res, r)
	}
	return res, nil
}

// Report writes Markdown.
func Report(w io.Writer, res []Result, dataset string) {
	failed := 0
	for _, r := range res {
		if !r.OK() {
			failed++
		}
	}
	fmt.Fprintf(w, "# Query plans (EXPLAIN ANALYZE, BUFFERS)\n\n")
	fmt.Fprintf(w, "Generated by `library explain` (`make explain`) on %s.\n\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(w, "Dataset: %s\n\n", dataset)
	fmt.Fprintf(w, "Automatic checks: no `Seq Scan` on books/authors/book_authors/author_books/book_isbns/audit_log (incl. partitions), no `OFFSET` in SQL.\n\n")
	fmt.Fprintf(w, "**Result: %d/%d queries passed.**\n\n", len(res)-failed, len(res))
	fmt.Fprintf(w, "| # | Query | Exec time, ms | Seq Scan on big tables | OFFSET | Verdict |\n|---|---|---:|---|---|---|\n")
	for i, r := range res {
		v := "PASS"
		if !r.OK() {
			v = "FAIL"
		}
		ss := "none"
		if len(r.SeqScans) > 0 {
			ss = strings.Join(r.SeqScans, ", ")
		}
		fmt.Fprintf(w, "| %d | %s | %.2f | %s | %v | %s |\n", i+1, r.Name, r.ExecMS, ss, r.Offset, v)
	}
	for i, r := range res {
		fmt.Fprintf(w, "\n## %d. %s\n\n```sql\n%s\n```\n\nParameters: `%v`\n\n```\n%s\n```\n", i+1, r.Name, r.SQL, r.Args, r.Plan)
	}
}
