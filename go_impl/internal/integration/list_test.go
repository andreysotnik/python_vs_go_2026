package integration

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

type fixture struct {
	pub        string
	u1, u2     string
	dost, mura string
	mahf, anon string
	books      map[string]map[string]any // by title (unique ones)
	all        []string                  // ids of all fixture books
	start, mid time.Time
}

var fx *fixture

func getFixture(t *testing.T) *fixture {
	t.Helper()
	if fx != nil {
		return fx
	}
	f := &fixture{pub: fmt.Sprintf("PubIT %d", time.Now().UnixNano()), books: map[string]map[string]any{}}
	f.u1, f.u2 = newUser(t, "fx1"), newUser(t, "fx2")
	f.start = time.Now().Add(-time.Second)
	f.dost = mkAuthor(t, f.u1, map[string]any{"full_name": "Fyodor Dostoevsky", "original_name": "Фёдор Достоевский", "birth_date": "1821-11-11", "country_code": "RU"})
	f.mura = mkAuthor(t, f.u1, map[string]any{"full_name": "Haruki Murakami", "original_name": "村上春樹", "birth_date": "1949-01-12", "country_code": "JP"})
	f.mahf = mkAuthor(t, f.u2, map[string]any{"full_name": "Naguib Mahfouz", "original_name": "نجيب محفوظ", "birth_date": "1911-12-11", "country_code": "EG"})
	f.anon = mkAuthor(t, f.u2, map[string]any{"full_name": "Anonymous Zyxwv"})
	type bk struct {
		title, lang, user string
		year              any
		genres            []string
		authors           []string
		isbn              string
		orig              string
	}
	books := []bk{
		{"Crime and Punishment", "en", f.u1, 1866, []string{"classics", "fiction"}, []string{f.dost}, "9780140449136", "Преступление и наказание"},
		{"Преступление и наказание", "ru", f.u1, 1866, []string{"classics"}, []string{f.dost}, "", ""},
		{"Братья Карамазовы", "ru", f.u1, 1880, []string{"classics", "philosophy"}, []string{f.dost}, "", ""},
		{"ノルウェイの森", "ja", f.u2, 1987, []string{"fiction", "romance"}, []string{f.mura}, "", "Norwegian Wood"},
		{"Kafka on the Shore", "en", f.u2, 2002, []string{"fantasy", "fiction"}, []string{f.mura}, "", ""},
		{"زقاق المدق", "ar", f.u2, 1947, []string{"fiction"}, []string{f.mahf}, "", "Midaq Alley"},
		{"Les Misérables", "fr", f.u1, 1862, []string{"classics"}, nil, "", ""},
		{"Untitled Manuscript", "la", f.u2, nil, []string{"history"}, []string{f.anon}, "", ""},
		{"Lost Fragment", "grc", f.u2, nil, []string{"history", "poetry"}, []string{f.anon}, "", ""},
	}
	for i := 0; i < 5; i++ { // identical sort values: tie-break by id must keep pagination stable
		books = append(books, bk{"Same Title", "en", f.u1, 1999, []string{"mystery"}, nil, "", ""})
	}
	for i, b := range books {
		body := map[string]any{"title": b.title, "language_code": b.lang, "publisher": f.pub, "genres": b.genres, "publication_year": b.year}
		if b.isbn != "" {
			body["isbn_13"] = b.isbn
		}
		if b.orig != "" {
			body["original_title"] = b.orig
		}
		var links []map[string]any
		for _, a := range b.authors {
			links = append(links, map[string]any{"author_id": a})
		}
		body["authors"] = links
		created := mkBook(t, b.user, body)
		if i == 7 { // server clock (DB now()) is the reference, not the test process clock
			f.mid, _ = time.Parse(time.RFC3339Nano, created["created_at"].(string))
		}
		f.books[b.title] = created
		f.all = append(f.all, created["id"].(string))
	}
	fx = f
	return f
}

func setOf(xs []string) map[string]bool {
	m := map[string]bool{}
	for _, x := range xs {
		m[x] = true
	}
	return m
}

// filterIDs walks all pages of /books with the fixture publisher plus extra params.
func (f *fixture) filterIDs(t *testing.T, kv ...string) []string {
	t.Helper()
	q := qv(append([]string{"publisher", f.pub, "limit", "2"}, kv...)...)
	return ids(walk(t, "/api/v1/books", q))
}

func (f *fixture) idsOf(titles ...string) []string {
	out := []string{}
	for _, t := range titles {
		out = append(out, f.books[t]["id"].(string))
	}
	return out
}

func sameSet(t *testing.T, name string, got, want []string) {
	t.Helper()
	g, w := slices.Clone(got), slices.Clone(want)
	sort.Strings(g)
	sort.Strings(w)
	if !slices.Equal(g, w) {
		t.Fatalf("%s: got %v want %v", name, g, w)
	}
}

func TestBookFilters(t *testing.T) {
	f := getFixture(t)
	if got := f.filterIDs(t); len(got) != len(f.all) {
		t.Fatalf("publisher filter: %d vs %d", len(got), len(f.all))
	}
	sameSet(t, "title fuzzy", f.filterIDs(t, "title", "rime and punish"), f.idsOf("Crime and Punishment"))
	sameSet(t, "title unaccent", f.filterIDs(t, "title", "MISERABLES"), f.idsOf("Les Misérables"))
	sameSet(t, "title by original_title", f.filterIDs(t, "title", "norwegian"), f.idsOf("ノルウェイの森"))
	sameSet(t, "title cyrillic", f.filterIDs(t, "title", "наказан"), f.idsOf("Crime and Punishment", "Преступление и наказание"))
	sameSet(t, "title CJK", f.filterIDs(t, "title", "ウェイの"), f.idsOf("ノルウェイの森"))
	sameSet(t, "author name", f.filterIDs(t, "author", "dostoev"), f.idsOf("Crime and Punishment", "Преступление и наказание", "Братья Карамазовы"))
	sameSet(t, "author native", f.filterIDs(t, "author", "محفوظ"), f.idsOf("زقاق المدق"))
	sameSet(t, "author_id", f.filterIDs(t, "author_id", f.mura), f.idsOf("ノルウェイの森", "Kafka on the Shore"))
	sameSet(t, "isbn13", f.filterIDs(t, "isbn", "978-0-14-044913-6"), f.idsOf("Crime and Punishment"))
	sameSet(t, "isbn10", f.filterIDs(t, "isbn", "0140449132"), f.idsOf("Crime and Punishment"))
	sameSet(t, "language", f.filterIDs(t, "language", "RU"), f.idsOf("Преступление и наказание", "Братья Карамазовы"))
	sameSet(t, "year range", f.filterIDs(t, "year_from", "1860", "year_to", "1870"), f.idsOf("Crime and Punishment", "Преступление и наказание", "Les Misérables"))
	sameSet(t, "year_from", f.filterIDs(t, "year_from", "2000"), f.idsOf("Kafka on the Shore"))
	sameSet(t, "genre", f.filterIDs(t, "genre", "Fantasy"), f.idsOf("Kafka on the Shore"))
	sameSet(t, "genre+language", f.filterIDs(t, "genre", "classics", "language", "ru"), f.idsOf("Преступление и наказание", "Братья Карамазовы"))
	if got := f.filterIDs(t, "created_by", f.u2); len(got) != 5 {
		t.Fatalf("created_by: %d", len(got))
	}
	from := f.mid.UTC().Format(time.RFC3339Nano)
	if got := f.filterIDs(t, "created_from", from); len(got) != len(f.all)-7 {
		t.Fatalf("created_from: %d", len(got))
	}
	if got := f.filterIDs(t, "created_to", from); len(got) != 7 {
		t.Fatalf("created_to: %d", len(got))
	}
	if got := f.filterIDs(t, "created_from", f.start.UTC().Format("2006-01-02"), "created_to", time.Now().Add(48*time.Hour).UTC().Format("2006-01-02")); len(got) != len(f.all) {
		t.Fatalf("created date range: %d", len(got))
	}
	// full-text (simple config + unaccent, any script)
	sameSet(t, "q en", f.filterIDs(t, "q", "punishment"), f.idsOf("Crime and Punishment"))
	sameSet(t, "q ru", f.filterIDs(t, "q", "наказание"), f.idsOf("Crime and Punishment", "Преступление и наказание"))
	sameSet(t, "q unaccent", f.filterIDs(t, "q", "miserables"), f.idsOf("Les Misérables"))
	sameSet(t, "q websearch OR", f.filterIDs(t, "q", "kafka OR misérables"), f.idsOf("Kafka on the Shore", "Les Misérables"))
	sameSet(t, "q phrase exclude", f.filterIDs(t, "q", "title -same"), []string{})
	sameSet(t, "q arabic", f.filterIDs(t, "q", "المدق"), f.idsOf("زقاق المدق"))
	// combined filters
	sameSet(t, "combo", f.filterIDs(t, "language", "en", "year_from", "1999", "year_to", "1999", "genre", "mystery", "created_by", f.u1), f.filterIDs(t, "title", "same title"))

	// total estimate
	p := getPage(t, "/api/v1/books", qv("publisher", f.pub, "include_total", "true"))
	if p.Total == nil || !p.TotalIsEstimate {
		t.Fatalf("expected estimated total: %+v", p)
	}
	p = getPage(t, "/api/v1/books", qv("publisher", f.pub))
	if p.Total != nil || p.TotalIsEstimate {
		t.Fatal("total must be null without include_total")
	}
}

func TestBookSortsAndKeyset(t *testing.T) {
	f := getFixture(t)
	for _, s := range []string{"title", "-title", "publication_year", "-publication_year", "created_at", "-created_at"} {
		t.Run(s, func(t *testing.T) {
			all := walk(t, "/api/v1/books", qv("publisher", f.pub, "sort", s, "limit", "100"))
			for _, lim := range []string{"1", "2", "3"} {
				paged := walk(t, "/api/v1/books", qv("publisher", f.pub, "sort", s, "limit", lim))
				if !slices.Equal(ids(paged), ids(all)) {
					t.Fatalf("limit %s: paged order differs\n%v\n%v", lim, ids(paged), ids(all))
				}
			}
			if len(all) != len(f.all) || len(setOf(ids(all))) != len(f.all) {
				t.Fatalf("duplicates or gaps: %d", len(all))
			}
			field := strings.TrimPrefix(s, "-")
			desc := strings.HasPrefix(s, "-")
			nullSeen := false
			for i := 1; i < len(all); i++ {
				a, b := all[i-1][field], all[i][field]
				if b == nil {
					nullSeen = true
					continue
				}
				if nullSeen {
					t.Fatalf("NULLs must be last (%s)", s)
				}
				if a == nil {
					continue
				}
				if c := compare(a, b); (desc && c < 0) || (!desc && c > 0) {
					t.Fatalf("order broken at %d: %v then %v", i, a, b)
				}
			}
			if field == "publication_year" && all[len(all)-1]["publication_year"] != nil {
				t.Fatal("null years expected at the end")
			}
		})
	}
	// ties: 5 identical titles walked with limit 2 – each exactly once, ordered by id
	same := ids(walk(t, "/api/v1/books", qv("publisher", f.pub, "title", "same title", "sort", "title", "limit", "2")))
	if len(same) != 5 || !slices.IsSorted(same) {
		t.Fatalf("tie-break: %v", same)
	}
}

func compare(a, b any) int {
	switch x := a.(type) {
	case float64:
		y := b.(float64)
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		}
		return 0
	case string:
		if ta, err := time.Parse(time.RFC3339Nano, x); err == nil {
			tb, _ := time.Parse(time.RFC3339Nano, b.(string))
			return ta.Compare(tb)
		}
		return 0 // text collation is checked via paged == unpaged equality
	}
	return 0
}

func TestListErrors(t *testing.T) {
	f := getFixture(t)
	bad := []string{
		"limit=0", "limit=101", "limit=abc", "sort=publisher", "sort=-isbn", "cursor=garbage", "unknown=1",
		"title=ab", "author=xy", "author_id=-1", "isbn=123", "language=english", "year_from=x", "year_from=2000&year_to=1990",
		"created_from=yesterday", "created_by=0", "created_from=2025-01-02&created_to=2025-01-01", "include_total=maybe", "q=", "limit=1&limit=2",
	}
	for _, b := range bad {
		r := expect(t, call(t, "GET", "/api/v1/books?"+b, nil, nil), 400)
		if len(r.m(t)["errors"].([]any)) == 0 {
			t.Fatalf("%s: field errors missing", b)
		}
	}
	// cursor issued for one sort cannot be used with another
	p := getPage(t, "/api/v1/books", qv("publisher", f.pub, "sort", "title", "limit", "1"))
	expect(t, call(t, "GET", "/api/v1/books?sort=-title&cursor="+*p.NextCursor, nil, nil), 400)
	for _, b := range []string{"name=ab", "country=RUS", "born_from=1990", "born_from=2000-01-01&born_to=1990-01-01", "sort=title"} {
		expect(t, call(t, "GET", "/api/v1/authors?"+b, nil, nil), 400)
	}
	for _, b := range []string{"", "q=x&type=films", "q=x&limit=500", "q=x&cursor=zzz"} {
		expect(t, call(t, "GET", "/api/v1/search?"+b, nil, nil), 400)
	}
	expect(t, call(t, "GET", "/api/v1/authors/1/books?sort=title", nil, nil), 400)
}

func TestAuthorFiltersAndSorts(t *testing.T) {
	f := getFixture(t)
	mine := setOf([]string{f.dost, f.mura, f.mahf, f.anon})
	only := func(items []map[string]any) []string {
		var out []string
		for _, id := range ids(items) {
			if mine[id] {
				out = append(out, id)
			}
		}
		return out
	}
	sameSet(t, "name", only(walk(t, "/api/v1/authors", qv("name", "murakam", "limit", "100"))), []string{f.mura})
	sameSet(t, "name native", only(walk(t, "/api/v1/authors", qv("name", "достоевск", "limit", "100"))), []string{f.dost})
	sameSet(t, "name CJK", only(walk(t, "/api/v1/authors", qv("name", "村上春", "limit", "100"))), []string{f.mura})
	sameSet(t, "country", only(walk(t, "/api/v1/authors", qv("country", "eg", "limit", "100"))), []string{f.mahf})
	sameSet(t, "born range", only(walk(t, "/api/v1/authors", qv("born_from", "1900-01-01", "born_to", "1920-01-01", "limit", "100"))), []string{f.mahf})
	sameSet(t, "q", only(walk(t, "/api/v1/authors", qv("q", "zyxwv", "limit", "100"))), []string{f.anon})
	sameSet(t, "q native", only(walk(t, "/api/v1/authors", qv("q", "Фёдор", "limit", "100"))), []string{f.dost})

	for _, s := range []string{"full_name", "-full_name", "birth_date", "-birth_date", "created_at", "-created_at"} {
		all := walk(t, "/api/v1/authors", qv("sort", s, "limit", "100"))
		paged := walk(t, "/api/v1/authors", qv("sort", s, "limit", "3"))
		if !slices.Equal(ids(all), ids(paged)) || len(setOf(ids(all))) != len(all) {
			t.Fatalf("author keyset %s inconsistent", s)
		}
		if strings.HasSuffix(s, "birth_date") && all[len(all)-1]["birth_date"] != nil {
			t.Fatalf("NULL birth dates must be last for %s", s)
		}
		if got := only(all); len(got) != 4 {
			t.Fatalf("sort %s lost fixture authors", s)
		}
	}
	p := getPage(t, "/api/v1/authors", qv("include_total", "true", "country", "RU"))
	if p.Total == nil || !p.TotalIsEstimate {
		t.Fatal("author total estimate missing")
	}
}

func TestSearch(t *testing.T) {
	f := getFixture(t)
	r := getPage(t, "/api/v1/search", qv("q", "Dostoevsky", "type", "authors"))
	if len(r.Items) == 0 || r.Items[0]["id"] != f.dost || r.Items[0]["type"] != "author" {
		t.Fatalf("author search: %v", r.Items)
	}
	r = getPage(t, "/api/v1/search", qv("q", "Murakam", "type", "authors")) // prefix / typo via trigram
	if len(r.Items) == 0 || r.Items[0]["id"] != f.mura {
		t.Fatalf("fuzzy author search: %v", r.Items)
	}
	r = getPage(t, "/api/v1/search", qv("q", "Punishmentt", "type", "books")) // typo
	found := false
	for _, it := range r.Items {
		if it["id"] == f.books["Crime and Punishment"]["id"] {
			found = true
		}
	}
	if !found {
		t.Fatalf("typo-tolerant book search failed: %v", r.Items)
	}
	r = getPage(t, "/api/v1/search", qv("q", "наказание"))
	if len(r.Items) < 2 {
		t.Fatalf("cyrillic search: %v", r.Items)
	}
	// keyset over (rank, id) in mixed results: no duplicates, non-increasing rank
	all := walk(t, "/api/v1/search", qv("q", "Same Title", "type", "all", "limit", "1"))
	one := walk(t, "/api/v1/search", qv("q", "Same Title", "type", "all", "limit", "100"))
	if !slices.Equal(ids(all), ids(one)) || len(setOf(ids(all))) != len(all) || len(all) == 0 {
		t.Fatalf("search pagination: %v vs %v", ids(all), ids(one))
	}
	for i := 1; i < len(all); i++ {
		if all[i]["rank"].(float64) > all[i-1]["rank"].(float64) {
			t.Fatal("rank must be non-increasing")
		}
	}
	types := map[string]bool{}
	for _, it := range walk(t, "/api/v1/search", qv("q", "murakami OR kafka", "limit", "100")) {
		types[it["type"].(string)] = true
	}
	if !types["book"] || !types["author"] {
		t.Fatalf("type=all must mix books and authors: %v", types)
	}
}
