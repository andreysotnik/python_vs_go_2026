package domain

import (
	"encoding/json"
	"strconv"
	"time"
)

// ID is a 63-bit snowflake id serialized as a JSON string (exceeds JS safe integer range).
type ID int64

// MarshalJSON encodes the id as a string.
func (id ID) MarshalJSON() ([]byte, error) {
	return []byte(`"` + strconv.FormatInt(int64(id), 10) + `"`), nil
}

// UnmarshalJSON accepts both "123" and 123.
func (id *ID) UnmarshalJSON(b []byte) error {
	s := string(b)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v <= 0 {
		return &json.UnsupportedValueError{Str: "id must be a positive integer (string)"}
	}
	*id = ID(v)
	return nil
}

// ParseID parses a path/query id.
func ParseID(s string) (ID, bool) {
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v <= 0 {
		return 0, false
	}
	return ID(v), true
}

// Audit holds who/when fields shared by books and authors.
type Audit struct {
	CreatedAt time.Time `json:"created_at"`
	CreatedBy ID        `json:"created_by"`
	UpdatedAt time.Time `json:"updated_at"`
	UpdatedBy ID        `json:"updated_by"`
	Version   int       `json:"version"`
}

// User is someone who changes catalog data.
type User struct {
	ID        ID        `json:"id"`
	Username  string    `json:"username"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
}

// Book roles.
var BookRoles = map[string]bool{"author": true, "co-author": true, "translator": true, "editor": true, "illustrator": true}

// BookAuthorRef is an author attached to a book (in book responses).
type BookAuthorRef struct {
	AuthorID ID      `json:"author_id"`
	FullName string  `json:"full_name"`
	Original *string `json:"original_name"`
	Role     string  `json:"role"`
	Ordinal  int     `json:"ordinal"`
}

// Book entity.
type Book struct {
	ID              ID                `json:"id"`
	Title           string            `json:"title"`
	OriginalTitle   *string           `json:"original_title"`
	Subtitle        *string           `json:"subtitle"`
	Description     *string           `json:"description"`
	ISBN10          *string           `json:"isbn_10"`
	ISBN13          *string           `json:"isbn_13"`
	LanguageCode    string            `json:"language_code"`
	PublicationYear *int              `json:"publication_year"`
	Publisher       *string           `json:"publisher"`
	PageCount       *int              `json:"page_count"`
	Genres          []string          `json:"genres"`
	Edition         *string           `json:"edition"`
	ExternalIDs     map[string]string `json:"external_ids"`
	Authors         []BookAuthorRef   `json:"authors"`
	Audit
	DeletedAt *time.Time `json:"-"`
}

// Author entity.
type Author struct {
	ID           ID                `json:"id"`
	FullName     string            `json:"full_name"`
	OriginalName *string           `json:"original_name"`
	BirthDate    *Date             `json:"birth_date"`
	DeathDate    *Date             `json:"death_date"`
	CountryCode  *string           `json:"country_code"`
	Biography    *string           `json:"biography"`
	ExternalIDs  map[string]string `json:"external_ids"`
	Audit
	DeletedAt *time.Time `json:"-"`
}

// AuthorBook is an entry of GET /authors/{id}/books.
type AuthorBook struct {
	Book
	Role    string `json:"role"`
	Ordinal int    `json:"ordinal"`
}

// AuditEntry is a row of the audit log.
type AuditEntry struct {
	ID         ID              `json:"id"`
	EntityType string          `json:"entity_type"`
	EntityID   ID              `json:"entity_id"`
	Action     string          `json:"action"`
	ChangedBy  ID              `json:"changed_by"`
	ChangedAt  time.Time       `json:"changed_at"`
	Diff       json.RawMessage `json:"diff"`
}

// SearchHit is a row of the global search.
type SearchHit struct {
	Type     string  `json:"type"`
	ID       ID      `json:"id"`
	Label    string  `json:"label"`
	Original *string `json:"original"`
	Rank     float64 `json:"rank"`
}

// Page is the uniform list envelope.
type Page[T any] struct {
	Items           []T     `json:"items"`
	NextCursor      *string `json:"next_cursor"`
	Limit           int     `json:"limit"`
	Total           *int64  `json:"total"`
	TotalIsEstimate bool    `json:"total_is_estimate"`
}
