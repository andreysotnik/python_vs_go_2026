package integration

import (
	"fmt"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestHealthDocsMetrics(t *testing.T) {
	expect(t, call(t, "GET", "/healthz", nil, nil), 200)
	expect(t, call(t, "GET", "/readyz", nil, nil), 200)
	r := expect(t, call(t, "GET", "/openapi.yaml", nil, nil), 200)
	if !strings.Contains(string(r.Body), "openapi: 3.1") {
		t.Fatal("openapi.yaml must be an OpenAPI 3.1 document")
	}
	r = expect(t, call(t, "GET", "/docs", nil, nil), 200)
	if !strings.Contains(string(r.Body), "swagger-ui") {
		t.Fatal("swagger ui missing")
	}
	r = expect(t, call(t, "GET", "/metrics", nil, nil), 200)
	for _, m := range []string{"http_requests_total", "http_request_duration_seconds", "db_pool_total_conns"} {
		if !strings.Contains(string(r.Body), m) {
			t.Fatalf("metric %s missing", m)
		}
	}
	expect(t, call(t, "GET", "/api/v1/nope", nil, nil), 404)
	expect(t, call(t, "POST", "/healthz", nil, nil), 405)
	expect(t, call(t, "GET", "/api/v1/books/abc", nil, nil), 404)
}

func TestUsers(t *testing.T) {
	name := fmt.Sprintf("alice_%d", time.Now().UnixNano()%1e9)
	r := expect(t, call(t, "POST", "/api/v1/users", map[string]any{"username": name, "email": name + "@ex.org"}, nil), 201)
	id := r.m(t)["id"].(string)
	if r.Header.Get("Location") != "/api/v1/users/"+id {
		t.Fatal("Location header missing")
	}
	expect(t, call(t, "POST", "/api/v1/users", map[string]any{"username": name, "email": "other" + name + "@ex.org"}, nil), 409)
	p := expect(t, call(t, "POST", "/api/v1/users", map[string]any{"username": "a", "email": "nope"}, nil), 422).m(t)
	if len(p["errors"].([]any)) != 2 {
		t.Fatalf("expected 2 field errors: %v", p)
	}
	got := expect(t, call(t, "GET", "/api/v1/users/"+id, nil, nil), 200).m(t)
	if got["username"] != name {
		t.Fatal("wrong user")
	}
	expect(t, call(t, "GET", "/api/v1/users/999", nil, nil), 404)
}

func TestAuthorCRUDAndAudit(t *testing.T) {
	u1, u2 := newUser(t, "creator"), newUser(t, "editor")
	body := map[string]any{"full_name": "Fyodor Dostoevsky", "original_name": "Фёдор Достоевский", "birth_date": "1821-11-11",
		"death_date": "1881-02-09", "country_code": "ru", "external_ids": map[string]string{"wikidata": "Q991"}}

	// authentication
	expect(t, call(t, "POST", "/api/v1/authors", body, nil), 401)
	expect(t, call(t, "POST", "/api/v1/authors", body, as("abc")), 401)
	expect(t, call(t, "POST", "/api/v1/authors", body, as("123456789")), 401)

	r := expect(t, call(t, "POST", "/api/v1/authors", body, as(u1)), 201)
	a := r.m(t)
	id := a["id"].(string)
	if a["created_by"] != u1 || a["updated_by"] != u1 || a["country_code"] != "RU" || a["version"].(float64) != 1 {
		t.Fatalf("unexpected author %v", a)
	}
	if r.Header.Get("ETag") != `"1"` {
		t.Fatalf("ETag = %q", r.Header.Get("ETag"))
	}
	g := expect(t, call(t, "GET", "/api/v1/authors/"+id, nil, nil), 200)
	if g.Header.Get("ETag") != `"1"` || g.m(t)["created_at"] == nil {
		t.Fatal("audit fields / etag missing")
	}

	// validation
	bad := []map[string]any{
		{"full_name": ""},
		{"full_name": "X", "country_code": "RUS"},
		{"full_name": "X", "birth_date": "1900-01-01", "death_date": "1800-01-01"},
		{"full_name": "X", "birth_date": "2999-01-01"},
		{"full_name": "X", "external_ids": map[string]string{"Bad Key": "1"}},
	}
	for _, b := range bad {
		expect(t, call(t, "POST", "/api/v1/authors", b, as(u1)), 422)
	}
	expect(t, call(t, "POST", "/api/v1/authors", `{"full_name": "X", "unknown": 1}`, as(u1)), 400)
	expect(t, call(t, "POST", "/api/v1/authors", `{"full_name": `, as(u1)), 400)
	expect(t, call(t, "POST", "/api/v1/authors", `{"full_name": 5}`, as(u1)), 400)
	expect(t, call(t, "POST", "/api/v1/authors", `{"birth_date": "11/11/1821"}`, as(u1)), 400)

	// optimistic locking
	expect(t, call(t, "PATCH", "/api/v1/authors/"+id, map[string]any{"biography": "x"}, as(u2)), 428)
	expect(t, call(t, "PATCH", "/api/v1/authors/"+id, map[string]any{"biography": "x"}, hdr{"X-User-Id": u2, "If-Match": "zz"}), 400)
	p := expect(t, call(t, "PATCH", "/api/v1/authors/"+id, map[string]any{"biography": "Russian novelist", "original_name": nil},
		hdr{"X-User-Id": u2, "If-Match": `"1"`}), 200)
	pm := p.m(t)
	if pm["version"].(float64) != 2 || pm["updated_by"] != u2 || pm["created_by"] != u1 || pm["original_name"] != nil || pm["full_name"] != "Fyodor Dostoevsky" {
		t.Fatalf("patch result %v", pm)
	}
	expect(t, call(t, "PATCH", "/api/v1/authors/"+id, map[string]any{"biography": "y"}, hdr{"X-User-Id": u2, "If-Match": `"1"`}), 409)
	// PUT with version in body replaces everything
	put := expect(t, call(t, "PUT", "/api/v1/authors/"+id, map[string]any{"full_name": "F. M. Dostoevsky", "version": 2}, as(u1)), 200).m(t)
	if put["biography"] != nil || put["birth_date"] != nil || put["version"].(float64) != 3 {
		t.Fatalf("PUT must replace: %v", put)
	}
	expect(t, call(t, "PUT", "/api/v1/authors/"+id, map[string]any{"full_name": "", "version": 3}, as(u1)), 422)

	// soft delete
	expect(t, call(t, "DELETE", "/api/v1/authors/"+id, nil, hdr{"X-User-Id": u2, "If-Match": `"1"`}), 409)
	expect(t, call(t, "DELETE", "/api/v1/authors/"+id, nil, as(u2)), 204)
	expect(t, call(t, "GET", "/api/v1/authors/"+id, nil, nil), 404)
	expect(t, call(t, "DELETE", "/api/v1/authors/"+id, nil, as(u2)), 404)
	expect(t, call(t, "PATCH", "/api/v1/authors/"+id, map[string]any{"biography": "z"}, hdr{"X-User-Id": u2, "If-Match": "4"}), 404)

	// history survives soft delete; newest first, with who/when
	h := walk(t, "/api/v1/authors/"+id+"/history", qv("limit", "2"))
	if len(h) != 4 {
		t.Fatalf("expected 4 history entries, got %d", len(h))
	}
	wantActions := []string{"delete", "update", "update", "create"}
	wantBy := []string{u2, u1, u2, u1}
	for i, e := range h {
		if e["action"] != wantActions[i] || e["changed_by"] != wantBy[i] || e["changed_at"] == nil {
			t.Fatalf("history[%d] = %v", i, e)
		}
	}
	diff := h[2]["diff"].(map[string]any)
	if _, ok := diff["biography"]; !ok {
		t.Fatalf("update diff must contain biography: %v", diff)
	}
	expect(t, call(t, "GET", "/api/v1/authors/42/history", nil, nil), 404)
	expect(t, call(t, "GET", "/api/v1/authors/"+id+"/history?cursor=bad", nil, nil), 400)
}

func mkAuthor(t *testing.T, user string, body map[string]any) string {
	t.Helper()
	return expect(t, call(t, "POST", "/api/v1/authors", body, as(user)), 201).m(t)["id"].(string)
}

func mkBook(t *testing.T, user string, body map[string]any) map[string]any {
	t.Helper()
	return expect(t, call(t, "POST", "/api/v1/books", body, as(user)), 201).m(t)
}

func TestBookCRUDAndAudit(t *testing.T) {
	u1, u2 := newUser(t, "bk1"), newUser(t, "bk2")
	a1 := mkAuthor(t, u1, map[string]any{"full_name": "Haruki Murakami", "original_name": "村上春樹", "country_code": "JP"})
	a2 := mkAuthor(t, u1, map[string]any{"full_name": "Jay Rubin"})

	b := mkBook(t, u1, map[string]any{"title": "Norwegian Wood", "original_title": "ノルウェイの森", "language_code": "EN",
		"isbn_10": "0-375-70402-7", "publication_year": 2000, "genres": []string{"Fiction", "fiction", "romance"},
		"authors": []map[string]any{{"author_id": a1}, {"author_id": a2, "role": "translator"}}})
	id := b["id"].(string)
	if b["isbn_13"] != "9780375704024" || b["isbn_10"] != "0375704027" || b["language_code"] != "en" {
		t.Fatalf("isbn/language normalization failed: %v", b)
	}
	if g := b["genres"].([]any); len(g) != 2 {
		t.Fatalf("genres not normalized: %v", g)
	}
	authors := b["authors"].([]any)
	if len(authors) != 2 || authors[1].(map[string]any)["role"] != "translator" || authors[0].(map[string]any)["original_name"] != "村上春樹" {
		t.Fatalf("authors: %v", authors)
	}
	if b["created_by"] != u1 || b["updated_by"] != u1 {
		t.Fatal("audit fields missing")
	}

	// uniqueness of ISBN across partitions
	expect(t, call(t, "POST", "/api/v1/books", map[string]any{"title": "Dup", "language_code": "en", "isbn_13": "978-0-375-70402-4"}, as(u1)), 409)
	// validation
	for _, bad := range []map[string]any{
		{"title": "x", "language_code": "en", "isbn_13": "9780375704025"},
		{"title": "x", "language_code": "en", "isbn_10": "0375704027", "isbn_13": "9780306406157"},
		{"title": "x", "language_code": "english"},
		{"title": "", "language_code": "en"},
		{"title": "x", "language_code": "en", "publication_year": 3000},
		{"title": "x", "language_code": "en", "page_count": 0},
		{"title": "x", "language_code": "en", "genres": []string{"Not a slug!"}},
		{"title": "x", "language_code": "en", "authors": []map[string]any{{"author_id": "1234567"}}},
		{"title": "x", "language_code": "en", "authors": []map[string]any{{"author_id": a1, "role": "ghost"}}},
		{"title": "x", "language_code": "en", "authors": []map[string]any{{"author_id": a1}, {"author_id": a1}}},
	} {
		expect(t, call(t, "POST", "/api/v1/books", bad, as(u1)), 422)
	}
	expect(t, call(t, "POST", "/api/v1/books", map[string]any{"title": "x", "language_code": "en", "authors": []map[string]any{{"author_id": "abc"}}}, as(u1)), 400)
	expect(t, call(t, "POST", "/api/v1/books", map[string]any{"title": "x"}, hdr{"X-User-Id": u1, "Content-Type": "text/plain"}), 400)

	// card
	card := expect(t, call(t, "GET", "/api/v1/books/"+id, nil, nil), 200)
	if card.Header.Get("ETag") != `"1"` {
		t.Fatal("etag")
	}

	// PATCH: change ISBN (old one is released), replace authors
	p := expect(t, call(t, "PATCH", "/api/v1/books/"+id, map[string]any{"isbn_10": nil, "isbn_13": "9780306406157", "subtitle": "A novel",
		"authors": []map[string]any{{"author_id": a1}}}, hdr{"X-User-Id": u2, "If-Match": `W/"1"`}), 200).m(t)
	if p["isbn_13"] != "9780306406157" || p["version"].(float64) != 2 || p["updated_by"] != u2 || len(p["authors"].([]any)) != 1 {
		t.Fatalf("patch: %v", p)
	}
	other := mkBook(t, u2, map[string]any{"title": "Reuses released ISBN", "language_code": "en", "isbn_13": "9780375704024"})
	expect(t, call(t, "PATCH", "/api/v1/books/"+other["id"].(string), map[string]any{"isbn_13": "9780306406157", "version": 1}, as(u2)), 409)

	// concurrent updates with the same version: exactly one wins
	var wg sync.WaitGroup
	codes := make([]int, 5)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = call(t, "PATCH", "/api/v1/books/"+id, map[string]any{"edition": fmt.Sprintf("e%d", i)}, hdr{"X-User-Id": u1, "If-Match": "2"}).Status
		}(i)
	}
	wg.Wait()
	ok, conflict := 0, 0
	for _, c := range codes {
		switch c {
		case 200:
			ok++
		case 409:
			conflict++
		}
	}
	if ok != 1 || conflict != 4 {
		t.Fatalf("optimistic locking: %v", codes)
	}

	// author's books (both directions)
	ab := walk(t, "/api/v1/authors/"+a1+"/books", qv("limit", "1"))
	if len(ab) != 1 || ab[0]["id"] != id || ab[0]["role"] != "author" {
		t.Fatalf("author books: %v", ab)
	}
	if got := walk(t, "/api/v1/authors/"+a2+"/books", nil); len(got) != 0 {
		t.Fatalf("translator link must be removed: %v", got)
	}
	expect(t, call(t, "GET", "/api/v1/authors/999/books", nil, nil), 404)

	// PUT replaces (authors cleared)
	put := expect(t, call(t, "PUT", "/api/v1/books/"+id, map[string]any{"title": "Norwegian Wood", "language_code": "ja", "version": 3}, as(u1)), 200).m(t)
	if put["subtitle"] != nil || len(put["authors"].([]any)) != 0 || put["isbn_13"] != nil {
		t.Fatalf("put: %v", put)
	}

	// soft delete hides from lists and card
	expect(t, call(t, "DELETE", "/api/v1/books/"+id, nil, hdr{"X-User-Id": u2, "If-Match": "4"}), 204)
	expect(t, call(t, "GET", "/api/v1/books/"+id, nil, nil), 404)
	expect(t, call(t, "PUT", "/api/v1/books/"+id, map[string]any{"title": "x", "language_code": "en", "version": 5}, as(u1)), 404)
	for _, it := range walk(t, "/api/v1/books", qv("title", "norwegian", "limit", "100")) {
		if it["id"] == id {
			t.Fatal("deleted book listed")
		}
	}
	h := walk(t, "/api/v1/books/"+id+"/history", nil)
	actions := []string{}
	for _, e := range h {
		actions = append(actions, e["action"].(string))
	}
	if strings.Join(actions, ",") != "delete,update,update,update,create" {
		t.Fatalf("history actions %v", actions)
	}
	if h[0]["changed_by"] != u2 || h[len(h)-1]["changed_by"] != u1 {
		t.Fatal("history must record who changed the book")
	}
	created := h[len(h)-1]["diff"].(map[string]any)
	if created["title"].(map[string]any)["new"] != "Norwegian Wood" || created["authors"] == nil {
		t.Fatalf("create diff %v", created)
	}
	expect(t, call(t, "GET", "/api/v1/books/77/history", nil, nil), 404)
	_ = url.Values{}
}
