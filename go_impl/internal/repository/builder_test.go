package repository

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/example/worldlib/internal/cursor"
	"github.com/example/worldlib/internal/domain"
)

var offsetRe = regexp.MustCompile(`(?i)\boffset\b`)

func lp(sort string, limit int, c *cursor.Cursor) domain.ListParams {
	return domain.ListParams{Sort: domain.SortKey{Field: strings.TrimLeft(sort, "-"), Desc: strings.HasPrefix(sort, "-")}, Limit: limit, Cursor: c}
}

func sqls(t *testing.T, p KeysetPlan, err error) ([]string, [][]any) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	q, a, err := p.SQL()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range q {
		if offsetRe.MatchString(s) {
			t.Fatalf("OFFSET generated: %s", s)
		}
		if !strings.Contains(s, "LIMIT") {
			t.Fatalf("no LIMIT: %s", s)
		}
	}
	return q, a
}

func TestKeysetNonNullable(t *testing.T) {
	q, a := check(t)((BuildListBooks(domain.BookFilter{}, lp("title", 20, nil))))
	if len(q) != 1 || !strings.Contains(q[0], "ORDER BY b.title ASC, b.id ASC LIMIT 21") || strings.Contains(q[0], "(b.title, b.id)") {
		t.Fatalf("%s", q[0])
	}
	_ = a
	c := &cursor.Cursor{Sort: "-title", Value: cursor.StrPtr("M"), ID: 9}
	q, a = check(t)((BuildListBooks(domain.BookFilter{}, lp("-title", 5, c))))
	if !strings.Contains(q[0], "(b.title, b.id) < ($1, $2)") || !strings.Contains(q[0], "ORDER BY b.title DESC, b.id DESC LIMIT 6") || a[0][0] != "M" || a[0][1] != int64(9) {
		t.Fatalf("%s %v", q[0], a)
	}
	ts := "2025-01-02T03:04:05.123456Z"
	q, a = check(t)((BuildListBooks(domain.BookFilter{}, lp("created_at", 5, &cursor.Cursor{Sort: "created_at", Value: &ts, ID: 1}))))
	if _, ok := a[0][0].(time.Time); !ok || !strings.Contains(q[0], "(b.created_at, b.id) > ($1, $2)") {
		t.Fatalf("%s %v", q[0], a)
	}
	// NULL-phase cursor on a NOT NULL column is invalid
	_, err := BuildListBooks(domain.BookFilter{}, lp("title", 5, &cursor.Cursor{Sort: "title", ID: 1}))
	assertBadRequest(t, err)
	bad := "x"
	_, err = BuildListBooks(domain.BookFilter{}, lp("created_at", 5, &cursor.Cursor{Sort: "created_at", Value: &bad, ID: 1}))
	assertBadRequest(t, err)
}

// check adapts (plan, err) results for sqls.
func check(t *testing.T) func(KeysetPlan, error) ([]string, [][]any) {
	return func(p KeysetPlan, err error) ([]string, [][]any) { return sqls(t, p, err) }
}

func assertBadRequest(t *testing.T, err error) {
	t.Helper()
	var ve *domain.ValidationError
	if !errors.As(err, &ve) || ve.Kind != domain.KindBadRequest {
		t.Fatalf("expected 400 validation error, got %v", err)
	}
}

func TestKeysetNullable(t *testing.T) {
	p, err := BuildListBooks(domain.BookFilter{}, lp("-publication_year", 10, nil))
	q, _ := sqls(t, p, err)
	if len(q) != 2 || !strings.Contains(q[0], "b.publication_year IS NOT NULL") || !strings.Contains(q[1], "b.publication_year IS NULL") ||
		!strings.Contains(q[1], "ORDER BY b.publication_year DESC, b.id DESC") {
		t.Fatalf("%v", q)
	}
	p, err = BuildListBooks(domain.BookFilter{}, lp("publication_year", 10, &cursor.Cursor{Sort: "publication_year", Value: cursor.StrPtr("1990"), ID: 3}))
	q, a := sqls(t, p, err)
	if len(q) != 2 || !strings.Contains(q[0], "(b.publication_year, b.id) > ($1, $2)") || a[0][0] != 1990 {
		t.Fatalf("%v %v", q, a)
	}
	p, err = BuildListBooks(domain.BookFilter{}, lp("publication_year", 10, &cursor.Cursor{Sort: "publication_year", ID: 3}))
	q, a = sqls(t, p, err)
	if len(q) != 1 || !strings.Contains(q[0], "b.publication_year IS NULL AND b.id > $1") || a[0][0] != int64(3) {
		t.Fatalf("%v %v", q, a)
	}
	_, err = BuildListBooks(domain.BookFilter{}, lp("publication_year", 10, &cursor.Cursor{Sort: "publication_year", Value: cursor.StrPtr("abc"), ID: 3}))
	assertBadRequest(t, err)
	d := "1900-01-02"
	p, err = BuildListAuthors(domain.AuthorFilter{}, lp("birth_date", 10, &cursor.Cursor{Sort: "birth_date", Value: &d, ID: 3}))
	q, a = sqls(t, p, err)
	if !strings.Contains(q[0], "(a.birth_date, a.id) > ($1, $2)") || a[0][0].(time.Time).Year() != 1900 {
		t.Fatalf("%v", q)
	}
	bd := "1900"
	_, err = BuildListAuthors(domain.AuthorFilter{}, lp("birth_date", 10, &cursor.Cursor{Sort: "birth_date", Value: &bd, ID: 3}))
	assertBadRequest(t, err)
}

func TestBookWhereAllFilters(t *testing.T) {
	s := func(v string) *string { return &v }
	i := func(v int) *int { return &v }
	id := domain.ID(5)
	now := time.Now()
	f := domain.BookFilter{Title: s("50%_off\\"), Author: s("tol"), AuthorID: &id, ISBN13: s("9780306406157"), Language: s("ru"),
		YearFrom: i(1), YearTo: i(2), Publisher: s("P"), Genre: s("g"), CreatedBy: &id, CreatedFrom: &now, CreatedTo: &now, Q: s("war")}
	q, args, err := psql.Select("1").From("books b").Where(BookWhere(f)).ToSql()
	if err != nil {
		t.Fatal(err)
	}
	for _, frag := range []string{"b.deleted_at IS NULL", "LIKE '%' || norm_text($1) || '%'", "author_books ab WHERE ab.author_id IN",
		"LIMIT 1000", "book_isbns bi", "b.language_code =", "b.publication_year >=", "b.publication_year <=", "b.publisher =",
		"b.genres @> ARRAY[", "b.created_by =", "b.created_at >=", "b.created_at <", "websearch_to_tsquery('simple_unaccent'"} {
		if !strings.Contains(q, frag) {
			t.Errorf("missing %q in %s", frag, q)
		}
	}
	if args[0] != `50\%\_off\\` || len(args) != 13 {
		t.Fatalf("args %v", args)
	}
	aq, aargs, _ := psql.Select("1").From("authors a").Where(AuthorWhere(domain.AuthorFilter{Name: s("x_y"), Country: s("RU"),
		BornFrom: &domain.Date{Time: now}, BornTo: &domain.Date{Time: now}, Q: s("q")})).ToSql()
	if len(aargs) != 5 || !strings.Contains(aq, "a.country_code =") || aargs[0] != `x\_y` {
		t.Fatalf("%s %v", aq, aargs)
	}
}

func TestSortValues(t *testing.T) {
	y := 1999
	ts := time.Date(2025, 1, 2, 3, 4, 5, 6000, time.UTC)
	b := domain.Book{Title: "T", PublicationYear: &y, Audit: domain.Audit{CreatedAt: ts}}
	if *BookSortValue(b, "title") != "T" || *BookSortValue(b, "publication_year") != "1999" || *BookSortValue(b, "created_at") != "2025-01-02T03:04:05.000006Z" {
		t.Fatal("book sort values")
	}
	if BookSortValue(domain.Book{}, "publication_year") != nil {
		t.Fatal("null year")
	}
	a := domain.Author{FullName: "N", BirthDate: &domain.Date{Time: ts}, Audit: domain.Audit{CreatedAt: ts}}
	if *AuthorSortValue(a, "full_name") != "N" || *AuthorSortValue(a, "birth_date") != "2025-01-02" || AuthorSortValue(domain.Author{}, "birth_date") != nil {
		t.Fatal("author sort values")
	}
	if *AuthorSortValue(a, "created_at") == "" || *FormatSortValue(0.25) != "0.25" || FormatSortValue(struct{}{}) != nil || FormatSortValue(nil) != nil {
		t.Fatal("format")
	}
}

func TestSearchAndHistoryBuilders(t *testing.T) {
	for _, typ := range []string{"books", "authors", "all"} {
		q, args, err := BuildSearch(domain.SearchParams{Q: "war", Type: typ, Limit: 10})
		if err != nil || offsetRe.MatchString(q) || !strings.Contains(q, "ORDER BY s.rank DESC, s.id DESC LIMIT $2") || len(args) != 2 {
			t.Fatalf("%s: %v %s", typ, err, q)
		}
		if (typ == "all") != strings.Contains(q, "UNION ALL") {
			t.Fatal("union only for all")
		}
	}
	c := &cursor.Cursor{Sort: "rank:all", Value: cursor.StrPtr("0.5"), ID: 7}
	q, args, err := BuildSearch(domain.SearchParams{Q: "war", Type: "all", Limit: 10, Cursor: c})
	if err != nil || !strings.Contains(q, "(s.rank, s.id) < ($2::float8, $3::bigint)") || args[1] != 0.5 || args[3] != 11 {
		t.Fatalf("%v %s %v", err, q, args)
	}
	_, _, err = BuildSearch(domain.SearchParams{Q: "w", Type: "all", Cursor: &cursor.Cursor{ID: 1}})
	assertBadRequest(t, err)
	_, _, err = BuildSearch(domain.SearchParams{Q: "w", Type: "all", Cursor: &cursor.Cursor{ID: 1, Value: cursor.StrPtr("x")}})
	assertBadRequest(t, err)
	if !strings.HasPrefix(SearchCursor(domain.SearchParams{Type: "all"}, domain.SearchHit{ID: 1, Rank: 0.1}), "ey") {
		t.Fatal("search cursor")
	}

	h, err := BuildHistory("book", 1, &cursor.Cursor{Value: cursor.StrPtr("2025-01-01T00:00:00Z"), ID: 2}, 5)
	if err != nil {
		t.Fatal(err)
	}
	hq, _, _ := h.ToSql()
	if !strings.Contains(hq, "(changed_at, id) < ($3, $4)") || !strings.Contains(hq, "LIMIT 6") {
		t.Fatal(hq)
	}
	_, err = BuildHistory("book", 1, &cursor.Cursor{ID: 2}, 5)
	assertBadRequest(t, err)
	_, err = BuildHistory("book", 1, &cursor.Cursor{ID: 2, Value: cursor.StrPtr("x")}, 5)
	assertBadRequest(t, err)

	ab, _, _ := BuildAuthorBooks(3, &cursor.Cursor{Value: cursor.StrPtr("author"), ID: 4}, 10).ToSql()
	if !strings.Contains(ab, "(ab.book_id, ab.role) > ($2, $3)") || !strings.Contains(ab, "ORDER BY ab.book_id, ab.role LIMIT 11") {
		t.Fatal(ab)
	}
}

func TestEscapeLikeAndMapErr(t *testing.T) {
	if escapeLike(`a%b_c\`) != `a\%b\_c\\` {
		t.Fatal(escapeLike(`a%b_c\`))
	}
	if mapErr(nil) != nil {
		t.Fatal("nil")
	}
	other := errors.New("x")
	if !errors.Is(mapErr(other), other) {
		t.Fatal("passthrough")
	}
}
