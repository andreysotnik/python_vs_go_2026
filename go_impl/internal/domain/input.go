package domain

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/example/worldlib/internal/isbn"
)

// AuthorLink links an author to a book.
type AuthorLink struct {
	AuthorID ID     `json:"author_id"`
	Role     string `json:"role"`
	Ordinal  int    `json:"ordinal"`
}

// BookInput is the request body for POST/PUT/PATCH /books.
type BookInput struct {
	Title           Opt[string]            `json:"title"`
	OriginalTitle   Opt[string]            `json:"original_title"`
	Subtitle        Opt[string]            `json:"subtitle"`
	Description     Opt[string]            `json:"description"`
	ISBN10          Opt[string]            `json:"isbn_10"`
	ISBN13          Opt[string]            `json:"isbn_13"`
	LanguageCode    Opt[string]            `json:"language_code"`
	PublicationYear Opt[int]               `json:"publication_year"`
	Publisher       Opt[string]            `json:"publisher"`
	PageCount       Opt[int]               `json:"page_count"`
	Genres          Opt[[]string]          `json:"genres"`
	Edition         Opt[string]            `json:"edition"`
	ExternalIDs     Opt[map[string]string] `json:"external_ids"`
	Authors         Opt[[]AuthorLink]      `json:"authors"`
	Version         Opt[int]               `json:"version"`
}

// AuthorInput is the request body for POST/PUT/PATCH /authors.
type AuthorInput struct {
	FullName     Opt[string]            `json:"full_name"`
	OriginalName Opt[string]            `json:"original_name"`
	BirthDate    Opt[Date]              `json:"birth_date"`
	DeathDate    Opt[Date]              `json:"death_date"`
	CountryCode  Opt[string]            `json:"country_code"`
	Biography    Opt[string]            `json:"biography"`
	ExternalIDs  Opt[map[string]string] `json:"external_ids"`
	Version      Opt[int]               `json:"version"`
}

// UserInput is the request body for POST /users.
type UserInput struct {
	Username string `json:"username"`
	Email    string `json:"email"`
}

// Mode tells how an input is applied.
type Mode int

// Apply modes.
const (
	ModeCreate Mode = iota
	ModeReplace
	ModePatch
)

var (
	reLang     = regexp.MustCompile(`^[a-z]{2,3}$`)
	reCountry  = regexp.MustCompile(`^[A-Z]{2}$`)
	reGenre    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	reExtKey   = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)
	reUsername = regexp.MustCompile(`^[A-Za-z0-9_.-]{3,64}$`)
	reEmail    = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
)

// Limits.
const (
	MaxAuthorsPerBook = 100
	MaxGenres         = 20
	MaxExternalIDs    = 20
	MinYear           = -3000
)

// normalizeOpt trims strings; an empty string becomes null.
func normalizeOpt(o Opt[string]) Opt[string] {
	if o.Set && !o.Null {
		o.Value = strings.TrimSpace(o.Value)
		if o.Value == "" {
			o.Null = true
		}
	}
	return o
}

// ApplyBook applies input to dst according to mode. It returns the requested author links
// (nil when the input does not touch authors). The result must be validated with ValidateBook.
func ApplyBook(dst *Book, in BookInput, mode Mode) *[]AuthorLink {
	if mode == ModeReplace {
		keep := *dst
		*dst = Book{ID: keep.ID, Audit: keep.Audit, DeletedAt: keep.DeletedAt}
	}
	if in.Title.Set {
		dst.Title = strings.TrimSpace(in.Title.Value)
	}
	applyPtr(&dst.OriginalTitle, normalizeOpt(in.OriginalTitle))
	applyPtr(&dst.Subtitle, normalizeOpt(in.Subtitle))
	applyPtr(&dst.Description, normalizeOpt(in.Description))
	applyPtr(&dst.ISBN10, normalizeOpt(in.ISBN10))
	applyPtr(&dst.ISBN13, normalizeOpt(in.ISBN13))
	if in.LanguageCode.Set {
		dst.LanguageCode = strings.ToLower(strings.TrimSpace(in.LanguageCode.Value))
	}
	applyPtr(&dst.PublicationYear, in.PublicationYear)
	applyPtr(&dst.Publisher, normalizeOpt(in.Publisher))
	applyPtr(&dst.PageCount, in.PageCount)
	if in.Genres.Set {
		dst.Genres = normalizeGenres(in.Genres.Value)
	}
	applyPtr(&dst.Edition, normalizeOpt(in.Edition))
	if in.ExternalIDs.Set {
		dst.ExternalIDs = in.ExternalIDs.Value
	}
	if dst.Genres == nil {
		dst.Genres = []string{}
	}
	if dst.ExternalIDs == nil {
		dst.ExternalIDs = map[string]string{}
	}
	if !in.Authors.Set {
		if mode == ModePatch {
			return nil
		}
		empty := []AuthorLink{}
		return &empty
	}
	links := make([]AuthorLink, 0, len(in.Authors.Value))
	for i, l := range in.Authors.Value {
		if l.Role == "" {
			l.Role = "author"
		}
		if l.Ordinal == 0 {
			l.Ordinal = i + 1
		}
		links = append(links, l)
	}
	return &links
}

func normalizeGenres(gs []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(gs))
	for _, g := range gs {
		g = strings.ToLower(strings.TrimSpace(g))
		if g == "" || seen[g] {
			continue
		}
		seen[g] = true
		out = append(out, g)
	}
	sort.Strings(out)
	return out
}

func checkLen(ve *ValidationError, field string, s *string, maxRunes int) {
	if s != nil && utf8.RuneCountInString(*s) > maxRunes {
		ve.Add(field, "must be at most "+itoa(maxRunes)+" characters")
	}
}

func itoa(i int) string { return strconv.Itoa(i) }

func validateExternalIDs(ve *ValidationError, m map[string]string) {
	if len(m) > MaxExternalIDs {
		ve.Add("external_ids", "too many identifiers (max 20)")
	}
	for k, v := range m {
		if !reExtKey.MatchString(k) {
			ve.Add("external_ids."+k, "key must match ^[a-z0-9_]{1,32}$")
		}
		if v == "" || utf8.RuneCountInString(v) > 256 {
			ve.Add("external_ids."+k, "value must be 1..256 characters")
		}
	}
}

// ValidateBook validates and canonicalizes a book (derives ISBN-13 from ISBN-10).
func ValidateBook(b *Book, links *[]AuthorLink, now time.Time) error {
	ve := &ValidationError{Kind: KindUnprocessable}
	if b.Title == "" {
		ve.Add("title", "is required")
	}
	checkLen(ve, "title", &b.Title, 1000)
	checkLen(ve, "original_title", b.OriginalTitle, 1000)
	checkLen(ve, "subtitle", b.Subtitle, 1000)
	checkLen(ve, "description", b.Description, 20000)
	checkLen(ve, "publisher", b.Publisher, 512)
	checkLen(ve, "edition", b.Edition, 128)
	if !reLang.MatchString(b.LanguageCode) {
		ve.Add("language_code", "is required and must be an ISO 639-1/639-2 code (2-3 lowercase letters)")
	}
	if b.PublicationYear != nil && (*b.PublicationYear < MinYear || *b.PublicationYear > now.Year()+5) {
		ve.Add("publication_year", "must be between -3000 and "+itoa(now.Year()+5))
	}
	if b.PageCount != nil && (*b.PageCount < 1 || *b.PageCount > 100000) {
		ve.Add("page_count", "must be between 1 and 100000")
	}
	if len(b.Genres) > MaxGenres {
		ve.Add("genres", "too many genres (max 20)")
	}
	for _, g := range b.Genres {
		if !reGenre.MatchString(g) {
			ve.Add("genres", "invalid genre slug: "+g)
		}
	}
	validateExternalIDs(ve, b.ExternalIDs)
	validateISBN(ve, b)
	if links != nil {
		validateLinks(ve, *links)
	}
	return ve.OrNil()
}

func validateISBN(ve *ValidationError, b *Book) {
	if b.ISBN10 != nil {
		n := isbn.Normalize(*b.ISBN10)
		if !isbn.Valid10(n) {
			ve.Add("isbn_10", "invalid ISBN-10 (format or check digit)")
		} else {
			b.ISBN10 = &n
		}
	}
	if b.ISBN13 != nil {
		n := isbn.Normalize(*b.ISBN13)
		if !isbn.Valid13(n) {
			ve.Add("isbn_13", "invalid ISBN-13 (format or check digit)")
			return
		}
		b.ISBN13 = &n
	}
	if b.ISBN10 != nil && isbn.Valid10(*b.ISBN10) {
		derived, _ := isbn.To13(*b.ISBN10)
		switch {
		case b.ISBN13 == nil:
			b.ISBN13 = &derived
		case *b.ISBN13 != derived:
			ve.Add("isbn_10", "does not match isbn_13")
		}
	}
}

func validateLinks(ve *ValidationError, links []AuthorLink) {
	if len(links) > MaxAuthorsPerBook {
		ve.Add("authors", "too many authors (max 100)")
	}
	seen := map[string]bool{}
	for i, l := range links {
		f := "authors[" + itoa(i) + "]"
		if l.AuthorID <= 0 {
			ve.Add(f+".author_id", "is required")
		}
		if !BookRoles[l.Role] {
			ve.Add(f+".role", "must be one of author, co-author, translator, editor, illustrator")
		}
		if l.Ordinal < 1 || l.Ordinal > 1000 {
			ve.Add(f+".ordinal", "must be between 1 and 1000")
		}
		key := itoa(int(l.AuthorID)) + "/" + l.Role
		if seen[key] {
			ve.Add(f, "duplicate author/role pair")
		}
		seen[key] = true
	}
}

// ApplyAuthor applies input to dst according to mode.
func ApplyAuthor(dst *Author, in AuthorInput, mode Mode) {
	if mode == ModeReplace {
		keep := *dst
		*dst = Author{ID: keep.ID, Audit: keep.Audit, DeletedAt: keep.DeletedAt}
	}
	if in.FullName.Set {
		dst.FullName = strings.TrimSpace(in.FullName.Value)
	}
	applyPtr(&dst.OriginalName, normalizeOpt(in.OriginalName))
	applyPtr(&dst.BirthDate, in.BirthDate)
	applyPtr(&dst.DeathDate, in.DeathDate)
	cc := normalizeOpt(in.CountryCode)
	cc.Value = strings.ToUpper(cc.Value)
	applyPtr(&dst.CountryCode, cc)
	applyPtr(&dst.Biography, normalizeOpt(in.Biography))
	if in.ExternalIDs.Set {
		dst.ExternalIDs = in.ExternalIDs.Value
	}
	if dst.ExternalIDs == nil {
		dst.ExternalIDs = map[string]string{}
	}
}

// ValidateAuthor validates an author.
func ValidateAuthor(a *Author, now time.Time) error {
	ve := &ValidationError{Kind: KindUnprocessable}
	if a.FullName == "" {
		ve.Add("full_name", "is required")
	}
	checkLen(ve, "full_name", &a.FullName, 512)
	checkLen(ve, "original_name", a.OriginalName, 512)
	checkLen(ve, "biography", a.Biography, 20000)
	if a.CountryCode != nil && !reCountry.MatchString(*a.CountryCode) {
		ve.Add("country_code", "must be an ISO 3166-1 alpha-2 code")
	}
	for _, d := range []struct {
		f string
		v *Date
	}{{"birth_date", a.BirthDate}, {"death_date", a.DeathDate}} {
		if d.v != nil && (d.v.Year() < 1 || d.v.After(now)) {
			ve.Add(d.f, "must be between 0001-01-01 and today")
		}
	}
	if a.BirthDate != nil && a.DeathDate != nil && a.DeathDate.Before(a.BirthDate.Time) {
		ve.Add("death_date", "must not be before birth_date")
	}
	validateExternalIDs(ve, a.ExternalIDs)
	return ve.OrNil()
}

// ValidateUser validates a user input.
func ValidateUser(in *UserInput) error {
	ve := &ValidationError{Kind: KindUnprocessable}
	in.Username = strings.TrimSpace(in.Username)
	in.Email = strings.TrimSpace(strings.ToLower(in.Email))
	if !reUsername.MatchString(in.Username) {
		ve.Add("username", "must be 3..64 characters of [A-Za-z0-9_.-]")
	}
	if len(in.Email) > 254 || !reEmail.MatchString(in.Email) {
		ve.Add("email", "must be a valid e-mail address")
	}
	return ve.OrNil()
}
