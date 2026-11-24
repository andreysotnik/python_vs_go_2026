// Package seed generates deterministic, realistic catalog data and bulk-loads it with COPY.
package seed

import (
	"math/rand/v2"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/example/worldlib/internal/idgen"
	"github.com/example/worldlib/internal/isbn"
)

// Extra seed node for book audit rows (authors' audit uses idgen.SeedNodeAudit).
const seedNodeAuditBooks = 1005

// Fixed time window so that SEED_RANDOM fully determines the dataset.
var (
	windowStart = time.Date(2023, 12, 1, 0, 0, 0, 0, time.UTC)
	windowEnd   = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
)

// Gen is a pure, deterministic generator: row i depends only on (seed, i).
type Gen struct {
	Seed     uint64
	NAuthors int
	NBooks   int
	UserIDs  []int64
	localeW  int
}

// NewGen builds a generator.
func NewGen(seed uint64, nAuthors, nBooks, nUsers int) *Gen {
	g := &Gen{Seed: seed, NAuthors: nAuthors, NBooks: nBooks}
	for k := 0; k < nUsers; k++ {
		g.UserIDs = append(g.UserIDs, idgen.Compose(int64(k+1), idgen.SeedNodeUsers, int64(k%4096)))
	}
	for _, l := range locales {
		g.localeW += l.weight
	}
	return g
}

const (
	streamAuthor uint64 = 0xA5A5_0000_0000_0001
	streamBook   uint64 = 0xB00C_0000_0000_0002
	streamUser   uint64 = 0x05E2_0000_0000_0003
)

func (g *Gen) rng(stream uint64, i int) *rand.Rand {
	return rand.New(rand.NewPCG(g.Seed^stream, uint64(i)))
}

// splitmix64 is a cheap deterministic hash.
func splitmix64(x uint64) uint64 {
	x += 0x9E3779B97F4A7C15
	x = (x ^ (x >> 30)) * 0xBF58476D1CE4E5B9
	x = (x ^ (x >> 27)) * 0x94D049BB133111EB
	return x ^ (x >> 31)
}

// rowTime spreads n rows monotonically over the window with deterministic jitter.
func (g *Gen) rowTime(i, n int, salt uint64) time.Time {
	span := windowEnd.Sub(windowStart)
	step := span / time.Duration(n)
	t := windowStart.Add(step * time.Duration(i))
	if half := int64(step / 2); half > 0 {
		t = t.Add(time.Duration(int64(splitmix64(g.Seed^salt^uint64(i)) % uint64(half))))
	}
	return t.Truncate(time.Microsecond)
}

func (g *Gen) authorTime(j int) time.Time { return g.rowTime(j, g.NAuthors, streamAuthor) }
func (g *Gen) bookTime(i int) time.Time   { return g.rowTime(i, g.NBooks, streamBook) }

// AuthorID returns the id of the j-th author.
func (g *Gen) AuthorID(j int) int64 {
	return idgen.FromTime(g.authorTime(j), idgen.SeedNodeAuthors, int64(j%4096))
}

// BookID returns the id of the i-th book.
func (g *Gen) BookID(i int) int64 {
	return idgen.FromTime(g.bookTime(i), idgen.SeedNodeBooks, int64(i%4096))
}

func (g *Gen) user(r *rand.Rand) int64 { return g.UserIDs[r.IntN(len(g.UserIDs))] }

func (g *Gen) pickLocale(r *rand.Rand) *locale {
	x := r.IntN(g.localeW)
	for i := range locales {
		if x < locales[i].weight {
			return &locales[i]
		}
		x -= locales[i].weight
	}
	return &locales[0]
}

func pick[T any](r *rand.Rand, xs []T) T { return xs[r.IntN(len(xs))] }

func ptr[T any](v T) *T { return &v }

// AuthorRow is one generated author.
type AuthorRow struct {
	ID           int64
	FullName     string
	OriginalName *string
	BirthDate    *time.Time
	DeathDate    *time.Time
	CountryCode  string
	Biography    *string
	ExternalIDs  map[string]string
	CreatedAt    time.Time
	CreatedBy    int64
}

// Author generates the j-th author.
func (g *Gen) Author(j int) AuthorRow {
	r := g.rng(streamAuthor, j)
	l := g.pickLocale(r)
	f, s := pick(r, l.firsts), pick(r, l.lasts)
	a := AuthorRow{ID: g.AuthorID(j), CountryCode: pick(r, l.countries), CreatedAt: g.authorTime(j), CreatedBy: g.user(r)}
	if l.familyFirst {
		a.FullName = s.latin + " " + f.latin
	} else {
		a.FullName = f.latin + " " + s.latin
	}
	if f.native != "" {
		sep := " "
		if l.code == "zh" || l.code == "ja" {
			sep = ""
		}
		if l.familyFirst {
			a.OriginalName = ptr(s.native + sep + f.native)
		} else {
			a.OriginalName = ptr(f.native + sep + s.native)
		}
	}
	if r.IntN(100) < 85 {
		y := 1500 + r.IntN(505)
		if r.IntN(100) < 70 {
			y = 1900 + r.IntN(105)
		}
		b := time.Date(y, time.Month(1+r.IntN(12)), 1+r.IntN(28), 0, 0, 0, 0, time.UTC)
		a.BirthDate = &b
		if y < 1950 && r.IntN(100) < 80 {
			d := b.AddDate(25+r.IntN(65), r.IntN(12), r.IntN(28))
			if d.Before(windowStart) {
				a.DeathDate = &d
			}
		}
	}
	if r.IntN(100) < 40 {
		a.Biography = ptr("Writer from " + a.CountryCode + ", author of works about " + pick(r, l.words) + " and " + pick(r, l.words) + ".")
	}
	a.ExternalIDs = map[string]string{}
	if r.IntN(100) < 60 {
		a.ExternalIDs["viaf"] = strconv.FormatUint(10000000+r.Uint64N(90000000), 10)
		a.ExternalIDs["wikidata"] = "Q" + strconv.FormatUint(1000+r.Uint64N(100000000), 10)
	}
	if r.IntN(100) < 30 {
		a.ExternalIDs["isni"] = "0000000" + strconv.FormatUint(100000000+r.Uint64N(900000000), 10)
	}
	return a
}

// LinkRow is a book-author relation.
type LinkRow struct {
	AuthorID int64
	Role     string
	Ordinal  int16
}

// BookRow is one generated book.
type BookRow struct {
	ID              int64
	Title           string
	OriginalTitle   *string
	Subtitle        *string
	Description     *string
	ISBN10          *string
	ISBN13          *string
	LanguageCode    string
	PublicationYear *int16
	Publisher       *string
	PageCount       *int32
	Genres          []string
	Edition         *string
	ExternalIDs     map[string]string
	CreatedAt       time.Time
	CreatedBy       int64
	Links           []LinkRow
}

func title(r *rand.Rand, l *locale) string {
	n := 2 + r.IntN(4)
	if l.joiner == "" {
		n = 2 + r.IntN(5)
	}
	ws := make([]string, n)
	for k := range ws {
		ws[k] = pick(r, l.words)
	}
	t := strings.Join(ws, l.joiner)
	if l.joiner == " " {
		r0, size := utf8.DecodeRuneInString(t)
		t = string(unicode.ToUpper(r0)) + t[size:]
	}
	return t
}

// authorIndex maps a Zipf rank to an author index via a bijection, so prolific authors are
// spread over the id space ("long tail": few authors own many books).
func (g *Gen) authorIndex(rank uint64) int {
	const p = 1_000_000_007
	return int((rank*p + 7) % uint64(g.NAuthors))
}

// ISBNFor returns the deterministic unique ISBN-13 of book i (bijection on 9-digit bodies).
func ISBNFor(i int) string {
	prefix := "978"
	k := uint64(i)
	if k >= 1_000_000_000 {
		prefix, k = "979", k-1_000_000_000
	}
	body := (k*7919 + 12345) % 1_000_000_000
	return isbn.Generate13(prefix, body)
}

// Book generates the i-th book.
func (g *Gen) Book(i int) BookRow {
	r := g.rng(streamBook, i)
	l := g.pickLocale(r)
	b := BookRow{ID: g.BookID(i), LanguageCode: l.code, CreatedAt: g.bookTime(i), CreatedBy: g.user(r)}
	b.Title = title(r, l)
	if r.IntN(100) < 10 {
		b.OriginalTitle = ptr(title(r, g.pickLocale(r)))
	}
	if r.IntN(100) < 20 {
		b.Subtitle = ptr(title(r, l))
	}
	if r.IntN(100) < 50 {
		b.Description = ptr("A story about " + pick(r, l.words) + ", " + pick(r, l.words) + " and " + pick(r, l.words) + ".")
	}
	if r.IntN(100) < 92 {
		s := ISBNFor(i)
		b.ISBN13 = &s
		if strings.HasPrefix(s, "978") && r.IntN(2) == 0 {
			if s10, err := isbn.To10(s); err == nil {
				b.ISBN10 = &s10
			}
		}
	}
	if r.IntN(100) < 96 {
		var y int
		switch x := r.IntN(100); {
		case x < 70:
			y = 1950 + r.IntN(77)
		case x < 95:
			y = 1800 + r.IntN(150)
		default:
			y = 1450 + r.IntN(350)
		}
		b.PublicationYear = ptr(int16(y))
	}
	if r.IntN(100) < 90 {
		b.Publisher = ptr(pick(r, publisherStems) + pick(r, publisherSuffixes))
	}
	if r.IntN(100) < 90 {
		b.PageCount = ptr(int32(40 + r.IntN(1200)))
	}
	ng := 1 + r.IntN(3)
	seen := map[string]bool{}
	for k := 0; k < ng; k++ {
		gname := pick(r, genres)
		if !seen[gname] {
			seen[gname] = true
			b.Genres = append(b.Genres, gname)
		}
	}
	if r.IntN(100) < 20 {
		b.Edition = ptr(pick(r, editions))
	}
	b.ExternalIDs = map[string]string{}
	if r.IntN(100) < 50 {
		b.ExternalIDs["openlibrary"] = "OL" + strconv.Itoa(i+1) + "M"
	}
	// authors: Zipf-distributed primary author, optional co-authors / translator / illustrator.
	z := rand.NewZipf(r, 1.07, 4, uint64(g.NAuthors-1))
	type key struct {
		id   int64
		role string
	}
	used := map[key]bool{}
	add := func(idx int, role string) {
		id := g.AuthorID(idx)
		if used[key{id, role}] {
			return
		}
		used[key{id, role}] = true
		b.Links = append(b.Links, LinkRow{AuthorID: id, Role: role, Ordinal: int16(len(b.Links) + 1)})
	}
	add(g.authorIndex(z.Uint64()), "author")
	switch x := r.IntN(100); {
	case x < 5:
		add(r.IntN(g.NAuthors), "co-author")
		add(r.IntN(g.NAuthors), "co-author")
	case x < 18:
		add(r.IntN(g.NAuthors), "co-author")
	}
	if l.code != "en" && r.IntN(100) < 10 {
		add(r.IntN(g.NAuthors), "translator")
	}
	if seen["children"] && r.IntN(100) < 30 {
		add(r.IntN(g.NAuthors), "illustrator")
	}
	return b
}
