package domain

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/example/worldlib/internal/cursor"
)

var now = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func fieldsOf(err error) map[string]bool {
	out := map[string]bool{}
	var ve *ValidationError
	if errors.As(err, &ve) {
		for _, e := range ve.Errors {
			out[e.Field] = true
		}
	}
	return out
}

func decodeBook(t *testing.T, s string) BookInput {
	t.Helper()
	var in BookInput
	if err := json.Unmarshal([]byte(s), &in); err != nil {
		t.Fatal(err)
	}
	return in
}

func TestOptTriState(t *testing.T) {
	in := decodeBook(t, `{"title":"T","subtitle":null}`)
	if !in.Title.Set || in.Title.Null || in.Title.Value != "T" {
		t.Fatal("title")
	}
	if !in.Subtitle.Set || !in.Subtitle.Null {
		t.Fatal("subtitle must be explicit null")
	}
	if in.Description.Set {
		t.Fatal("description must be absent")
	}
	if o := Some(3); !o.Set || o.Value != 3 {
		t.Fatal("Some")
	}
}

func TestApplyBookModes(t *testing.T) {
	sub := "old"
	b := Book{ID: 7, Title: "Old", Subtitle: &sub, LanguageCode: "en", Audit: Audit{Version: 3}}
	links := ApplyBook(&b, decodeBook(t, `{"title":"  New  ","subtitle":"  "}`), ModePatch)
	if links != nil || b.Title != "New" || b.Subtitle != nil || b.LanguageCode != "en" || b.Version != 3 {
		t.Fatalf("patch: %+v %v", b, links)
	}
	links = ApplyBook(&b, decodeBook(t, `{"title":"R","language_code":"RU","genres":["B"," a ","b",""],"authors":[{"author_id":"5"},{"author_id":6,"role":"translator","ordinal":9}]}`), ModeReplace)
	if b.ID != 7 || b.Version != 3 || b.LanguageCode != "ru" || strings.Join(b.Genres, ",") != "a,b" {
		t.Fatalf("replace: %+v", b)
	}
	if len(*links) != 2 || (*links)[0].Role != "author" || (*links)[0].Ordinal != 1 || (*links)[1].Ordinal != 9 {
		t.Fatalf("links: %+v", *links)
	}
	var c Book
	links = ApplyBook(&c, decodeBook(t, `{"title":"x","language_code":"en"}`), ModeCreate)
	if links == nil || len(*links) != 0 || c.Genres == nil || c.ExternalIDs == nil {
		t.Fatal("create defaults")
	}
}

func TestValidateBook(t *testing.T) {
	ok := func(js string) *Book {
		var b Book
		l := ApplyBook(&b, decodeBook(t, js), ModeCreate)
		if err := ValidateBook(&b, l, now); err != nil {
			t.Fatalf("%s: %v", js, err)
		}
		return &b
	}
	b := ok(`{"title":"x","language_code":"en","isbn_10":"0-306-40615-2"}`)
	if *b.ISBN13 != "9780306406157" || *b.ISBN10 != "0306406152" {
		t.Fatal("isbn derivation")
	}
	ok(`{"title":"x","language_code":"grc","publication_year":-500,"external_ids":{"openlibrary":"OL1M"}}`)
	cases := map[string]string{
		`{"title":"","language_code":"en"}`: "title",
		`{"title":"x"}`:                     "language_code",
		`{"title":"x","language_code":"en","isbn_13":"9780306406158"}`:                        "isbn_13",
		`{"title":"x","language_code":"en","isbn_10":"0306406153"}`:                           "isbn_10",
		`{"title":"x","language_code":"en","isbn_10":"0306406152","isbn_13":"9780140449136"}`: "isbn_10",
		`{"title":"x","language_code":"en","publication_year":-4000}`:                         "publication_year",
		`{"title":"x","language_code":"en","page_count":0}`:                                   "page_count",
		`{"title":"x","language_code":"en","genres":["bad genre"]}`:                           "genres",
		`{"title":"x","language_code":"en","external_ids":{"BAD":"1"}}`:                       "external_ids.BAD",
		`{"title":"x","language_code":"en","external_ids":{"ok":""}}`:                         "external_ids.ok",
		`{"title":"x","language_code":"en","authors":[{"author_id":"1","role":"ghost"}]}`:     "authors[0].role",
		`{"title":"x","language_code":"en","authors":[{"author_id":"1","ordinal":5000}]}`:     "authors[0].ordinal",
		`{"title":"x","language_code":"en","authors":[{"author_id":"1"},{"author_id":"1"}]}`:  "authors[1]",
		`{"title":"x","language_code":"en","edition":"` + strings.Repeat("e", 200) + `"}`:     "edition",
	}
	for js, field := range cases {
		var b Book
		l := ApplyBook(&b, decodeBook(t, js), ModeCreate)
		err := ValidateBook(&b, l, now)
		if !fieldsOf(err)[field] {
			t.Errorf("%s: expected error on %s, got %v", js, field, err)
		}
	}
	var b2 Book
	l := ApplyBook(&b2, BookInput{Title: Some("x"), LanguageCode: Some("en"), Authors: Some([]AuthorLink{{AuthorID: 0}})}, ModeCreate)
	if !fieldsOf(ValidateBook(&b2, l, now))["authors[0].author_id"] {
		t.Fatal("author id required")
	}
	many := make([]AuthorLink, 101)
	for i := range many {
		many[i] = AuthorLink{AuthorID: ID(i + 1), Role: "author", Ordinal: 1}
	}
	l = &many
	if !fieldsOf(ValidateBook(&Book{Title: "x", LanguageCode: "en"}, l, now))["authors"] {
		t.Fatal("too many authors")
	}
	if !fieldsOf(ValidateBook(&Book{Title: "x", LanguageCode: "en", Genres: make([]string, 21)}, nil, now))["genres"] {
		t.Fatal("too many genres")
	}
}

func TestAuthorApplyValidate(t *testing.T) {
	var in AuthorInput
	_ = json.Unmarshal([]byte(`{"full_name":" Leo ","country_code":"ru","birth_date":"1828-09-09","death_date":"1910-11-20"}`), &in)
	var a Author
	ApplyAuthor(&a, in, ModeCreate)
	if a.FullName != "Leo" || *a.CountryCode != "RU" || a.ExternalIDs == nil {
		t.Fatalf("%+v", a)
	}
	if err := ValidateAuthor(&a, now); err != nil {
		t.Fatal(err)
	}
	a.Version = 4
	ApplyAuthor(&a, AuthorInput{FullName: Some("New")}, ModeReplace)
	if a.BirthDate != nil || a.Version != 4 || a.CountryCode != nil {
		t.Fatal("replace must clear fields and keep audit")
	}
	bad := []struct {
		js, field string
	}{
		{`{"full_name":""}`, "full_name"},
		{`{"full_name":"x","country_code":"rus"}`, "country_code"},
		{`{"full_name":"x","birth_date":"2030-01-01"}`, "birth_date"},
		{`{"full_name":"x","birth_date":"1900-01-01","death_date":"1899-01-01"}`, "death_date"},
		{`{"full_name":"x","external_ids":{"k":""}}`, "external_ids.k"},
	}
	for _, c := range bad {
		var in AuthorInput
		if err := json.Unmarshal([]byte(c.js), &in); err != nil {
			t.Fatal(err)
		}
		var a Author
		ApplyAuthor(&a, in, ModeCreate)
		if !fieldsOf(ValidateAuthor(&a, now))[c.field] {
			t.Errorf("%s: expected %s", c.js, c.field)
		}
	}
	var d Date
	if err := d.UnmarshalJSON([]byte(`"bad"`)); err == nil {
		t.Fatal("bad date accepted")
	}
}

func TestValidateUser(t *testing.T) {
	in := UserInput{Username: " bob_1 ", Email: " Bob@Example.ORG "}
	if err := ValidateUser(&in); err != nil || in.Email != "bob@example.org" || in.Username != "bob_1" {
		t.Fatalf("%v %+v", err, in)
	}
	f := fieldsOf(ValidateUser(&UserInput{Username: "x", Email: "nope"}))
	if !f["username"] || !f["email"] {
		t.Fatal("user validation")
	}
}

func TestIDJSON(t *testing.T) {
	var id ID
	for _, s := range []string{`"910519675559874560"`, `910519675559874560`} {
		if err := json.Unmarshal([]byte(s), &id); err != nil || id != 910519675559874560 {
			t.Fatalf("%s: %v", s, err)
		}
	}
	for _, s := range []string{`"x"`, `-1`, `"0"`} {
		if err := json.Unmarshal([]byte(s), &id); err == nil {
			t.Fatalf("%s accepted", s)
		}
	}
	b, _ := json.Marshal(ID(5))
	if string(b) != `"5"` {
		t.Fatal(string(b))
	}
	if _, ok := ParseID("0"); ok {
		t.Fatal("0 accepted")
	}
}

func TestErrors(t *testing.T) {
	ve := &ValidationError{}
	if ve.OrNil() != nil {
		t.Fatal("empty must be nil")
	}
	ve.Add("a", "b")
	if !strings.Contains(ve.Error(), "a: b") {
		t.Fatal(ve.Error())
	}
	if err := Unprocessable("f", "m"); fieldsOf(err)["f"] != true {
		t.Fatal("unprocessable")
	}
}

func TestParseBookQuery(t *testing.T) {
	v, _ := url.ParseQuery("title=war&isbn=0-306-40615-2&language=RU&year_from=1800&year_to=1900&genre=Fantasy&created_from=2025-01-01&created_to=2025-02-01T00:00:00Z&q=x&sort=-title&limit=50&include_total=true&author_id=5&created_by=6&publisher=P&author=tolst")
	f, lp, err := ParseBookQuery(v)
	if err != nil {
		t.Fatal(err)
	}
	if *f.ISBN13 != "9780306406157" || *f.Language != "ru" || *f.Genre != "fantasy" || *f.YearFrom != 1800 || lp.Limit != 50 ||
		!lp.Sort.Desc || lp.Sort.Field != "title" || !lp.IncludeTotal || *f.AuthorID != 5 || *f.CreatedBy != 6 || f.CreatedTo == nil || f.CreatedFrom == nil {
		t.Fatalf("%+v %+v", f, lp)
	}
	_, lp, _ = ParseBookQuery(url.Values{})
	if lp.Sort.String() != "-created_at" || lp.Limit != DefaultLimit {
		t.Fatal("defaults")
	}
	c := cursor.Encode(cursor.Cursor{Sort: "title", Value: cursor.StrPtr("a"), ID: 1})
	v, _ = url.ParseQuery("sort=title&cursor=" + c)
	if _, lp, err := ParseBookQuery(v); err != nil || lp.Cursor == nil || lp.Cursor.ID != 1 {
		t.Fatal("cursor parse")
	}
	for _, bad := range []string{"x=1", "title=ab", "limit=0", "sort=foo", "cursor=x", "isbn=1", "language=1", "year_from=a",
		"author_id=x", "created_from=x", "year_from=5&year_to=1", "created_from=2025-02-01&created_to=2025-01-01", "include_total=x", "q=" + strings.Repeat("a", 300), "q=a&q=b", "genre="} {
		v, _ := url.ParseQuery(bad)
		_, _, err := ParseBookQuery(v)
		var ve *ValidationError
		if !errors.As(err, &ve) || ve.Kind != KindBadRequest {
			t.Errorf("%s: expected 400, got %v", bad, err)
		}
	}
}

func TestParseAuthorAndSearch(t *testing.T) {
	v, _ := url.ParseQuery("name=tol&country=ru&born_from=1800-01-01&born_to=1900-01-01&q=x&sort=birth_date")
	f, lp, err := ParseAuthorQuery(v)
	if err != nil || *f.Country != "RU" || f.BornFrom == nil || lp.Sort.Field != "birth_date" {
		t.Fatalf("%v %+v", err, f)
	}
	for _, bad := range []string{"country=RUS", "born_from=x", "born_from=1900-01-01&born_to=1800-01-01", "zzz=1", "name=a"} {
		v, _ := url.ParseQuery(bad)
		if _, _, err := ParseAuthorQuery(v); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	sp, err := ParseSearchQuery(url.Values{"q": {"war"}})
	if err != nil || sp.Type != "all" || sp.Limit != 20 {
		t.Fatal("search defaults")
	}
	for _, bad := range []string{"", "q=x&type=x", "q=x&foo=1", "q=x&limit=0", "q=x&cursor=zz", "q=x&type="} {
		v, _ := url.ParseQuery(bad)
		if _, err := ParseSearchQuery(v); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	lp, err = ParsePageQuery(url.Values{"limit": {"5"}}, "-changed_at")
	if err != nil || !lp.Sort.Desc || lp.Limit != 5 {
		t.Fatal("page query")
	}
	if _, err := ParsePageQuery(url.Values{"sort": {"x"}}, "book_id"); err == nil {
		t.Fatal("unknown param accepted")
	}
	if _, err := ParsePageQuery(url.Values{"limit": {"x"}}, "book_id"); err == nil {
		t.Fatal("bad limit accepted")
	}
}
