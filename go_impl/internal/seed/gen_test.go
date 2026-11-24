package seed

import (
	"reflect"
	"testing"
	"unicode/utf8"

	"github.com/example/worldlib/internal/isbn"
)

func TestDeterministic(t *testing.T) {
	g1, g2 := NewGen(7, 1000, 5000, 10), NewGen(7, 1000, 5000, 10)
	for _, i := range []int{0, 1, 999, 4999} {
		if !reflect.DeepEqual(g1.Book(i), g2.Book(i)) {
			t.Fatalf("book %d not deterministic", i)
		}
	}
	if !reflect.DeepEqual(g1.Author(5), g2.Author(5)) {
		t.Fatal("author not deterministic")
	}
	if reflect.DeepEqual(NewGen(8, 1000, 5000, 10).Book(3), g1.Book(3)) {
		t.Fatal("different seeds must differ")
	}
}

func TestIDsUniqueMonotonic(t *testing.T) {
	g := NewGen(1, 20000, 50000, 5)
	var prev int64
	for i := 0; i < g.NBooks; i++ {
		id := g.BookID(i)
		if id <= prev {
			t.Fatalf("book ids not increasing at %d", i)
		}
		prev = id
	}
	prev = 0
	for j := 0; j < g.NAuthors; j++ {
		id := g.AuthorID(j)
		if id <= prev {
			t.Fatalf("author ids not increasing at %d", j)
		}
		prev = id
	}
}

func TestISBNUniqueValid(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200000; i++ {
		s := ISBNFor(i)
		if !isbn.Valid13(s) || seen[s] {
			t.Fatalf("bad or duplicate isbn %s at %d", s, i)
		}
		seen[s] = true
	}
	if !isbn.Valid13(ISBNFor(1_000_000_001)) {
		t.Fatal("979 range invalid")
	}
}

func TestLongTailAndScripts(t *testing.T) {
	g := NewGen(42, 2000, 20000, 10)
	counts := map[int64]int{}
	langs := map[string]int{}
	for i := 0; i < g.NBooks; i++ {
		b := g.Book(i)
		if len(b.Links) == 0 || b.Links[0].Role != "author" {
			t.Fatal("each book needs a primary author")
		}
		counts[b.Links[0].AuthorID]++
		langs[b.LanguageCode]++
		if b.ISBN10 != nil {
			if c, _ := isbn.To13(*b.ISBN10); b.ISBN13 == nil || c != *b.ISBN13 {
				t.Fatal("isbn10/13 mismatch")
			}
		}
	}
	maxc := 0
	for _, c := range counts {
		maxc = max(maxc, c)
	}
	if maxc < 200 { // top author should own >1% of books
		t.Fatalf("distribution is not long-tailed: max=%d", maxc)
	}
	for _, l := range []string{"en", "ru", "zh", "ja", "ar"} {
		if langs[l] == 0 {
			t.Fatalf("no books in %s", l)
		}
	}
	natives := 0
	for j := 0; j < 500; j++ {
		if g.Author(j).OriginalName != nil {
			natives++
		}
	}
	if natives == 0 {
		t.Fatal("expected native-script names")
	}
}

func TestValidUTF8(t *testing.T) {
	g := NewGen(3, 500, 3000, 3)
	for i := 0; i < g.NBooks; i++ {
		b := g.Book(i)
		if !utf8.ValidString(b.Title) {
			t.Fatalf("invalid utf8 title %q", b.Title)
		}
	}
}
