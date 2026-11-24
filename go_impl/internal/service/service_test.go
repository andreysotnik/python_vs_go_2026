package service

import (
	"errors"
	"testing"

	"github.com/example/worldlib/internal/domain"
)

func TestResolveVersion(t *testing.T) {
	v := 3
	if got, err := ResolveVersion(&v, domain.Some(9)); err != nil || got != 3 {
		t.Fatal("If-Match must win")
	}
	if got, err := ResolveVersion(nil, domain.Some(9)); err != nil || got != 9 {
		t.Fatal("body version")
	}
	if _, err := ResolveVersion(nil, domain.Opt[int]{Set: true, Null: true}); !errors.Is(err, domain.ErrPreconditionRequired) {
		t.Fatal("428 expected")
	}
}

func TestDiffs(t *testing.T) {
	s1, s2 := "a", "b"
	old := domain.Book{Title: "T", Subtitle: &s1, Genres: []string{"x"}, Audit: domain.Audit{Version: 1}}
	nb := domain.Book{Title: "T", Subtitle: &s2, Genres: []string{"x", "y"}, Audit: domain.Audit{Version: 2}}
	d := Diff(old, nb)
	if len(d) != 2 || d["subtitle"].Old != "a" || d["subtitle"].New != "b" {
		t.Fatalf("%v", d)
	}
	if _, ok := d["version"]; ok {
		t.Fatal("audit fields must be excluded")
	}
	c := CreateDiff(domain.Book{Title: "T", Genres: []string{}, ExternalIDs: map[string]string{}})
	if len(c) != 1 || c["title"].New != "T" || c["title"].Old != nil {
		t.Fatalf("%v", c)
	}
	c = CreateDiff(domain.Book{Title: "T", Genres: []string{"g"}, ExternalIDs: map[string]string{"k": "v"}})
	if len(c) != 3 {
		t.Fatalf("%v", c)
	}
	if !equalLinks(nil, []domain.AuthorLink{}) || equalLinks([]domain.AuthorLink{{AuthorID: 1}}, []domain.AuthorLink{{AuthorID: 2}}) ||
		equalLinks([]domain.AuthorLink{{}}, nil) {
		t.Fatal("equalLinks")
	}
	if !eqPtr[string](nil, nil) || eqPtr(&s1, nil) || eqPtr(&s1, &s2) {
		t.Fatal("eqPtr")
	}
	if len(linksSnapshot(nil)) != 0 || len(refsToLinks([]domain.BookAuthorRef{{AuthorID: 1}})) != 1 {
		t.Fatal("links helpers")
	}
}
