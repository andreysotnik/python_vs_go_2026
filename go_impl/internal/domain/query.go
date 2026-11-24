package domain

import (
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/example/worldlib/internal/cursor"
	"github.com/example/worldlib/internal/isbn"
)

// Pagination limits.
const (
	DefaultLimit   = 20
	MaxLimit       = 100
	MinFuzzyLength = 3 // trigram search needs at least one full trigram
	MaxQueryLength = 256
)

// Sort whitelists: only fields backed by a composite (field, id) index.
var (
	BookSorts   = []string{"title", "publication_year", "created_at"}
	AuthorSorts = []string{"full_name", "birth_date", "created_at"}
)

// SortKey is a whitelisted sort field and direction.
type SortKey struct {
	Field string
	Desc  bool
}

// String renders "-field" / "field".
func (s SortKey) String() string {
	if s.Desc {
		return "-" + s.Field
	}
	return s.Field
}

// ListParams are common list parameters.
type ListParams struct {
	Sort         SortKey
	Limit        int
	Cursor       *cursor.Cursor
	IncludeTotal bool
}

// BookFilter holds GET /books filters.
type BookFilter struct {
	Title       *string
	Author      *string
	AuthorID    *ID
	ISBN13      *string
	Language    *string
	YearFrom    *int
	YearTo      *int
	Publisher   *string
	Genre       *string
	CreatedBy   *ID
	CreatedFrom *time.Time
	CreatedTo   *time.Time
	Q           *string
}

// AuthorFilter holds GET /authors filters.
type AuthorFilter struct {
	Name     *string
	Country  *string
	BornFrom *Date
	BornTo   *Date
	Q        *string
}

// SearchParams holds GET /search parameters.
type SearchParams struct {
	Q      string
	Type   string
	Limit  int
	Cursor *cursor.Cursor
}

func checkKnown(v url.Values, known ...string) error {
	for k := range v {
		if !slices.Contains(known, k) {
			return BadRequest(k, "unknown query parameter")
		}
	}
	return nil
}

func one(v url.Values, k string) (string, bool, error) {
	vals, ok := v[k]
	if !ok {
		return "", false, nil
	}
	if len(vals) != 1 {
		return "", false, BadRequest(k, "must be given once")
	}
	s := strings.TrimSpace(vals[0])
	if s == "" {
		return "", false, BadRequest(k, "must not be empty")
	}
	if utf8.RuneCountInString(s) > MaxQueryLength {
		return "", false, BadRequest(k, "is too long (max 256 characters)")
	}
	return s, true, nil
}

// ParseLimit parses ?limit (1..100, default 20).
func ParseLimit(v url.Values) (int, error) {
	s, ok, err := one(v, "limit")
	if err != nil || !ok {
		return DefaultLimit, err
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > MaxLimit {
		return 0, BadRequest("limit", "must be an integer between 1 and 100")
	}
	return n, nil
}

// ParseCursor decodes ?cursor bound to the given sort key.
func ParseCursor(v url.Values, sortKey string) (*cursor.Cursor, error) {
	s, ok, err := one(v, "cursor")
	if err != nil || !ok {
		return nil, err
	}
	c, err := cursor.Decode(s, sortKey)
	if err != nil {
		return nil, BadRequest("cursor", "invalid or expired cursor (it must come from next_cursor of the same query and sort)")
	}
	return &c, nil
}

// ParseListParams parses sort/limit/cursor/include_total.
func ParseListParams(v url.Values, allowed []string, def SortKey) (ListParams, error) {
	p := ListParams{Sort: def}
	s, ok, err := one(v, "sort")
	if err != nil {
		return p, err
	}
	if ok {
		k := SortKey{Field: strings.TrimLeft(s, "+-"), Desc: strings.HasPrefix(s, "-")}
		if !slices.Contains(allowed, k.Field) {
			return p, BadRequest("sort", "must be one of "+strings.Join(allowed, ", ")+" (prefix with - for descending)")
		}
		p.Sort = k
	}
	if p.Limit, err = ParseLimit(v); err != nil {
		return p, err
	}
	if p.Cursor, err = ParseCursor(v, p.Sort.String()); err != nil {
		return p, err
	}
	if s, ok, err := one(v, "include_total"); err != nil {
		return p, err
	} else if ok {
		b, err := strconv.ParseBool(s)
		if err != nil {
			return p, BadRequest("include_total", "must be true or false")
		}
		p.IncludeTotal = b
	}
	return p, nil
}

type parser struct {
	v   url.Values
	err error
}

func (p *parser) str(k string) *string {
	if p.err != nil {
		return nil
	}
	s, ok, err := one(p.v, k)
	if err != nil {
		p.err = err
		return nil
	}
	if !ok {
		return nil
	}
	return &s
}

func (p *parser) fuzzy(k string) *string {
	s := p.str(k)
	if s != nil && utf8.RuneCountInString(*s) < MinFuzzyLength {
		p.err = BadRequest(k, "must be at least 3 characters")
		return nil
	}
	return s
}

func (p *parser) int(k string, lo, hi int) *int {
	s := p.str(k)
	if s == nil {
		return nil
	}
	n, err := strconv.Atoi(*s)
	if err != nil || n < lo || n > hi {
		p.err = BadRequest(k, "must be an integer between "+strconv.Itoa(lo)+" and "+strconv.Itoa(hi))
		return nil
	}
	return &n
}

func (p *parser) id(k string) *ID {
	s := p.str(k)
	if s == nil {
		return nil
	}
	id, ok := ParseID(*s)
	if !ok {
		p.err = BadRequest(k, "must be a positive integer id")
		return nil
	}
	return &id
}

func (p *parser) time(k string) *time.Time {
	s := p.str(k)
	if s == nil {
		return nil
	}
	t, err := time.Parse(time.RFC3339, *s)
	if err != nil {
		if d, err2 := time.Parse(DateLayout, *s); err2 == nil {
			return &d
		}
		p.err = BadRequest(k, "must be an RFC 3339 timestamp or YYYY-MM-DD date")
		return nil
	}
	return &t
}

func (p *parser) date(k string) *Date {
	s := p.str(k)
	if s == nil {
		return nil
	}
	t, err := time.Parse(DateLayout, *s)
	if err != nil {
		p.err = BadRequest(k, "must be a YYYY-MM-DD date")
		return nil
	}
	return &Date{t}
}

// ParseBookQuery parses GET /books.
func ParseBookQuery(v url.Values) (BookFilter, ListParams, error) {
	var f BookFilter
	if err := checkKnown(v, "title", "author", "author_id", "isbn", "language", "year_from", "year_to",
		"publisher", "genre", "created_by", "created_from", "created_to", "q", "sort", "limit", "cursor", "include_total"); err != nil {
		return f, ListParams{}, err
	}
	p := &parser{v: v}
	f.Title = p.fuzzy("title")
	f.Author = p.fuzzy("author")
	f.AuthorID = p.id("author_id")
	if s := p.str("isbn"); s != nil {
		c, err := isbn.ToCanonical13(*s)
		if err != nil && p.err == nil {
			p.err = BadRequest("isbn", "must be a valid ISBN-10 or ISBN-13")
		}
		f.ISBN13 = &c
	}
	if s := p.str("language"); s != nil {
		l := strings.ToLower(*s)
		if !reLang.MatchString(l) && p.err == nil {
			p.err = BadRequest("language", "must be an ISO 639 code (2-3 letters)")
		}
		f.Language = &l
	}
	f.YearFrom = p.int("year_from", MinYear, 2100)
	f.YearTo = p.int("year_to", MinYear, 2100)
	f.Publisher = p.str("publisher")
	if s := p.str("genre"); s != nil {
		g := strings.ToLower(*s)
		f.Genre = &g
	}
	f.CreatedBy = p.id("created_by")
	f.CreatedFrom = p.time("created_from")
	f.CreatedTo = p.time("created_to")
	f.Q = p.str("q")
	if p.err != nil {
		return f, ListParams{}, p.err
	}
	if f.YearFrom != nil && f.YearTo != nil && *f.YearFrom > *f.YearTo {
		return f, ListParams{}, BadRequest("year_from", "must not be greater than year_to")
	}
	if f.CreatedFrom != nil && f.CreatedTo != nil && f.CreatedFrom.After(*f.CreatedTo) {
		return f, ListParams{}, BadRequest("created_from", "must not be after created_to")
	}
	lp, err := ParseListParams(v, BookSorts, SortKey{Field: "created_at", Desc: true})
	return f, lp, err
}

// ParseAuthorQuery parses GET /authors.
func ParseAuthorQuery(v url.Values) (AuthorFilter, ListParams, error) {
	var f AuthorFilter
	if err := checkKnown(v, "name", "country", "born_from", "born_to", "q", "sort", "limit", "cursor", "include_total"); err != nil {
		return f, ListParams{}, err
	}
	p := &parser{v: v}
	f.Name = p.fuzzy("name")
	if s := p.str("country"); s != nil {
		c := strings.ToUpper(*s)
		if !reCountry.MatchString(c) && p.err == nil {
			p.err = BadRequest("country", "must be an ISO 3166-1 alpha-2 code")
		}
		f.Country = &c
	}
	f.BornFrom = p.date("born_from")
	f.BornTo = p.date("born_to")
	f.Q = p.str("q")
	if p.err != nil {
		return f, ListParams{}, p.err
	}
	if f.BornFrom != nil && f.BornTo != nil && f.BornFrom.After(f.BornTo.Time) {
		return f, ListParams{}, BadRequest("born_from", "must not be after born_to")
	}
	lp, err := ParseListParams(v, AuthorSorts, SortKey{Field: "created_at", Desc: true})
	return f, lp, err
}

// ParsePageQuery parses limit/cursor only (sub-resources with a fixed order).
func ParsePageQuery(v url.Values, sortKey string) (ListParams, error) {
	if err := checkKnown(v, "limit", "cursor"); err != nil {
		return ListParams{}, err
	}
	lp := ListParams{Sort: SortKey{Field: strings.TrimLeft(sortKey, "-"), Desc: strings.HasPrefix(sortKey, "-")}}
	var err error
	if lp.Limit, err = ParseLimit(v); err != nil {
		return lp, err
	}
	lp.Cursor, err = ParseCursor(v, sortKey)
	return lp, err
}

// ParseSearchQuery parses GET /search.
func ParseSearchQuery(v url.Values) (SearchParams, error) {
	var sp SearchParams
	if err := checkKnown(v, "q", "type", "limit", "cursor"); err != nil {
		return sp, err
	}
	p := &parser{v: v}
	q := p.str("q")
	t := p.str("type")
	if p.err != nil {
		return sp, p.err
	}
	if q == nil {
		return sp, BadRequest("q", "is required")
	}
	sp.Q = *q
	sp.Type = "all"
	if t != nil {
		if !slices.Contains([]string{"books", "authors", "all"}, *t) {
			return sp, BadRequest("type", "must be one of books, authors, all")
		}
		sp.Type = *t
	}
	var err error
	if sp.Limit, err = ParseLimit(v); err != nil {
		return sp, err
	}
	sp.Cursor, err = ParseCursor(v, "rank:"+sp.Type)
	return sp, err
}
