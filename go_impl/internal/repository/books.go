package repository

import (
	"context"
	"errors"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"

	"github.com/example/worldlib/internal/domain"
)

// Expressions must match the expression indexes in migrations/00003_indexes.sql exactly.
const (
	BookNormExpr   = "norm_text(b.title || ' ' || coalesce(b.original_title, ''))"
	AuthorNormExpr = "norm_text(a.full_name || ' ' || coalesce(a.original_name, ''))"
	tsConfig       = "'simple_unaccent'"
	// MaxAuthorMatches caps the author-name sub-search used by GET /books?author=.
	MaxAuthorMatches = 1000
)

const bookCols = `b.id, b.title, b.original_title, b.subtitle, b.description, b.isbn_10, b.isbn_13,
 b.language_code, b.publication_year, b.publisher, b.page_count, b.genres, b.edition, b.external_ids,
 b.created_at, b.created_by, b.updated_at, b.updated_by, b.version, b.deleted_at`

func scanBook(row pgx.Row, extra ...any) (domain.Book, error) {
	var b domain.Book
	dest := []any{&b.ID, &b.Title, &b.OriginalTitle, &b.Subtitle, &b.Description, &b.ISBN10, &b.ISBN13,
		&b.LanguageCode, &b.PublicationYear, &b.Publisher, &b.PageCount, &b.Genres, &b.Edition, &b.ExternalIDs,
		&b.CreatedAt, &b.CreatedBy, &b.UpdatedAt, &b.UpdatedBy, &b.Version, &b.DeletedAt}
	err := row.Scan(append(dest, extra...)...)
	if b.Genres == nil {
		b.Genres = []string{}
	}
	if b.ExternalIDs == nil {
		b.ExternalIDs = map[string]string{}
	}
	return b, err
}

// BookWhere converts a filter into predicates (always including deleted_at IS NULL so the
// partial indexes apply).
func BookWhere(f domain.BookFilter) sq.And {
	w := sq.And{sq.Expr("b.deleted_at IS NULL")}
	if f.Title != nil {
		w = append(w, sq.Expr(BookNormExpr+` LIKE '%' || norm_text(?) || '%'`, escapeLike(*f.Title)))
	}
	if f.Author != nil {
		w = append(w, sq.Expr(`b.id IN (SELECT ab.book_id FROM author_books ab WHERE ab.author_id IN (
  SELECT a.id FROM authors a WHERE a.deleted_at IS NULL AND `+AuthorNormExpr+` LIKE '%' || norm_text(?) || '%' LIMIT 1000))`,
			escapeLike(*f.Author)))
	}
	if f.AuthorID != nil {
		w = append(w, sq.Expr("b.id IN (SELECT ab.book_id FROM author_books ab WHERE ab.author_id = ?)", int64(*f.AuthorID)))
	}
	if f.ISBN13 != nil {
		w = append(w, sq.Expr("b.id IN (SELECT bi.book_id FROM book_isbns bi WHERE bi.isbn_13 = ?)", *f.ISBN13))
	}
	if f.Language != nil {
		w = append(w, sq.Expr("b.language_code = ?", *f.Language))
	}
	if f.YearFrom != nil {
		w = append(w, sq.Expr("b.publication_year >= ?", *f.YearFrom))
	}
	if f.YearTo != nil {
		w = append(w, sq.Expr("b.publication_year <= ?", *f.YearTo))
	}
	if f.Publisher != nil {
		w = append(w, sq.Expr("b.publisher = ?", *f.Publisher))
	}
	if f.Genre != nil {
		w = append(w, sq.Expr("b.genres @> ARRAY[?]::text[]", *f.Genre))
	}
	if f.CreatedBy != nil {
		w = append(w, sq.Expr("b.created_by = ?", int64(*f.CreatedBy)))
	}
	if f.CreatedFrom != nil {
		w = append(w, sq.Expr("b.created_at >= ?", *f.CreatedFrom))
	}
	if f.CreatedTo != nil {
		w = append(w, sq.Expr("b.created_at < ?", *f.CreatedTo))
	}
	if f.Q != nil {
		w = append(w, sq.Expr("b.search_vector @@ websearch_to_tsquery("+tsConfig+", ?)", *f.Q))
	}
	return w
}

// BuildListBooks returns the keyset plan for GET /books.
func BuildListBooks(f domain.BookFilter, p domain.ListParams) (KeysetPlan, error) {
	base := psql.Select(bookCols).From("books b").Where(BookWhere(f))
	return buildKeyset(base, bookSortCols[p.Sort.Field], "b.id", p.Sort.Desc, p.Cursor, p.Limit)
}

// BookSortValue extracts the cursor value of a book for a sort field.
func BookSortValue(b domain.Book, field string) *string {
	switch field {
	case "title":
		return FormatSortValue(b.Title)
	case "publication_year":
		return FormatSortValue(b.PublicationYear)
	default:
		return FormatSortValue(b.CreatedAt)
	}
}

// ListBooks runs GET /books.
func (r *Repo) ListBooks(ctx context.Context, f domain.BookFilter, p domain.ListParams) ([]domain.Book, bool, error) {
	plan, err := BuildListBooks(f, p)
	if err != nil {
		return nil, false, err
	}
	return runKeyset(ctx, r.db, plan, func(rows pgx.Rows) (domain.Book, error) { return scanBook(rows) })
}

// EstimateBooks returns the planner estimate of matching books.
func (r *Repo) EstimateBooks(ctx context.Context, f domain.BookFilter) (int64, error) {
	return estimateRows(ctx, r.db, psql.Select("1").From("books b").Where(BookWhere(f)))
}

// GetBook loads a book; includeDeleted lets callers see soft-deleted rows. forUpdate locks it.
func (r *Repo) GetBook(ctx context.Context, id domain.ID, forUpdate bool) (domain.Book, error) {
	q := "SELECT " + bookCols + " FROM books b WHERE b.id = $1 AND b.deleted_at IS NULL"
	if forUpdate {
		q += " FOR UPDATE"
	}
	b, err := scanBook(r.db.QueryRow(ctx, q, int64(id)))
	return b, mapErr(err)
}

// BookExists reports whether a book row exists (including soft-deleted).
func (r *Repo) BookExists(ctx context.Context, id domain.ID) (bool, error) {
	var ok bool
	err := r.db.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM books WHERE id = $1)", int64(id)).Scan(&ok)
	return ok, mapErr(err)
}

// LoadBookAuthors fills Authors for the given books with two index-only-style lookups:
// links by book id (book_authors partitions pruned by the id array), then authors by id.
// A single JOIN was replaced because, for ~20+ ids, the planner preferred a hash join with a
// Seq Scan over every authors partition (cost model ignores run-time partition pruning).
func (r *Repo) LoadBookAuthors(ctx context.Context, books []domain.Book) error {
	if len(books) == 0 {
		return nil
	}
	ids := make([]int64, len(books))
	idx := make(map[domain.ID]int, len(books))
	for i := range books {
		ids[i] = int64(books[i].ID)
		idx[books[i].ID] = i
		books[i].Authors = []domain.BookAuthorRef{}
	}
	rows, err := r.db.Query(ctx, BookLinksSQL, ids)
	if err != nil {
		return mapErr(err)
	}
	type link struct {
		bookID, authorID domain.ID
		role             string
		ordinal          int
	}
	links, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (link, error) {
		var l link
		return l, row.Scan(&l.bookID, &l.authorID, &l.role, &l.ordinal)
	})
	if err != nil || len(links) == 0 {
		return mapErr(err)
	}
	authorIDs := make([]int64, 0, len(links))
	for _, l := range links {
		authorIDs = append(authorIDs, int64(l.authorID))
	}
	rows, err = r.db.Query(ctx, AuthorsByIDsSQL, authorIDs)
	if err != nil {
		return mapErr(err)
	}
	type name struct {
		full string
		orig *string
	}
	names := map[domain.ID]name{}
	for rows.Next() {
		var id domain.ID
		var n name
		if err := rows.Scan(&id, &n.full, &n.orig); err != nil {
			rows.Close()
			return err
		}
		names[id] = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return mapErr(err)
	}
	for _, l := range links { // links are ordered by (book_id, ordinal, author_id)
		n, ok := names[l.authorID]
		if !ok { // soft-deleted author
			continue
		}
		i := idx[l.bookID]
		books[i].Authors = append(books[i].Authors, domain.BookAuthorRef{
			AuthorID: l.authorID, FullName: n.full, Original: n.orig, Role: l.role, Ordinal: l.ordinal})
	}
	return nil
}

// SQL used by LoadBookAuthors (exported for the EXPLAIN checker).
const (
	BookLinksSQL = `SELECT ba.book_id, ba.author_id, ba.role, ba.ordinal FROM book_authors ba
WHERE ba.book_id = ANY($1) ORDER BY ba.book_id, ba.ordinal, ba.author_id`
	AuthorsByIDsSQL = `SELECT a.id, a.full_name, a.original_name FROM authors a WHERE a.id = ANY($1) AND a.deleted_at IS NULL`
)

// InsertBook inserts a book row (created/updated audit fields are set by the caller).
func (r *Repo) InsertBook(ctx context.Context, b *domain.Book) error {
	err := r.db.QueryRow(ctx, `INSERT INTO books (id, title, original_title, subtitle, description, isbn_10, isbn_13,
 language_code, publication_year, publisher, page_count, genres, edition, external_ids, created_by, updated_by)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$15)
RETURNING created_at, updated_at, version`,
		int64(b.ID), b.Title, b.OriginalTitle, b.Subtitle, b.Description, b.ISBN10, b.ISBN13, b.LanguageCode,
		b.PublicationYear, b.Publisher, b.PageCount, b.Genres, b.Edition, b.ExternalIDs, int64(b.CreatedBy),
	).Scan(&b.CreatedAt, &b.UpdatedAt, &b.Version)
	if err != nil {
		return mapErr(err)
	}
	b.UpdatedBy = b.CreatedBy
	return nil
}

// UpdateBook writes all mutable columns and bumps the version (optimistic locking guard included).
func (r *Repo) UpdateBook(ctx context.Context, b *domain.Book, expectedVersion int, by domain.ID) error {
	err := r.db.QueryRow(ctx, `UPDATE books SET title=$2, original_title=$3, subtitle=$4, description=$5, isbn_10=$6,
 isbn_13=$7, language_code=$8, publication_year=$9, publisher=$10, page_count=$11, genres=$12, edition=$13,
 external_ids=$14, updated_by=$15, updated_at=now(), version=version+1
WHERE id=$1 AND version=$16 AND deleted_at IS NULL
RETURNING updated_at, updated_by, version`,
		int64(b.ID), b.Title, b.OriginalTitle, b.Subtitle, b.Description, b.ISBN10, b.ISBN13, b.LanguageCode,
		b.PublicationYear, b.Publisher, b.PageCount, b.Genres, b.Edition, b.ExternalIDs, int64(by), expectedVersion,
	).Scan(&b.UpdatedAt, &b.UpdatedBy, &b.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrVersionConflict
	}
	return mapErr(err)
}

// SoftDeleteBook marks the book deleted.
func (r *Repo) SoftDeleteBook(ctx context.Context, id domain.ID, by domain.ID) (time.Time, error) {
	var at time.Time
	err := r.db.QueryRow(ctx, `UPDATE books SET deleted_at=now(), updated_at=now(), updated_by=$2, version=version+1
WHERE id=$1 AND deleted_at IS NULL RETURNING deleted_at`, int64(id), int64(by)).Scan(&at)
	return at, mapErr(err)
}

// ClaimISBN registers an ISBN-13 in the global uniqueness table.
func (r *Repo) ClaimISBN(ctx context.Context, isbn13 string, bookID domain.ID) error {
	_, err := r.db.Exec(ctx, "INSERT INTO book_isbns (isbn_13, book_id) VALUES ($1, $2)", isbn13, int64(bookID))
	return mapErr(err)
}

// ReleaseISBN frees an ISBN-13.
func (r *Repo) ReleaseISBN(ctx context.Context, isbn13 string, bookID domain.ID) error {
	_, err := r.db.Exec(ctx, "DELETE FROM book_isbns WHERE isbn_13 = $1 AND book_id = $2", isbn13, int64(bookID))
	return mapErr(err)
}

// MissingAuthors returns ids among the given that do not exist (or are deleted).
func (r *Repo) MissingAuthors(ctx context.Context, ids []int64) ([]int64, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := r.db.Query(ctx, "SELECT a.id FROM authors a WHERE a.id = ANY($1) AND a.deleted_at IS NULL", ids)
	if err != nil {
		return nil, mapErr(err)
	}
	found, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return nil, mapErr(err)
	}
	seen := make(map[int64]bool, len(found))
	for _, id := range found {
		seen[id] = true
	}
	var missing []int64
	for _, id := range ids {
		if !seen[id] {
			missing = append(missing, id)
			seen[id] = true
		}
	}
	return missing, nil
}

// ReplaceBookLinks replaces the book's authors in both directions (book_authors + author_books).
func (r *Repo) ReplaceBookLinks(ctx context.Context, bookID domain.ID, links []domain.AuthorLink) error {
	rows, err := r.db.Query(ctx, "DELETE FROM book_authors WHERE book_id = $1 RETURNING author_id", int64(bookID))
	if err != nil {
		return mapErr(err)
	}
	old, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return mapErr(err)
	}
	if len(old) > 0 {
		if _, err := r.db.Exec(ctx, "DELETE FROM author_books WHERE author_id = ANY($1) AND book_id = $2", old, int64(bookID)); err != nil {
			return mapErr(err)
		}
	}
	if len(links) == 0 {
		return nil
	}
	authorIDs := make([]int64, len(links))
	roles := make([]string, len(links))
	ords := make([]int32, len(links))
	for i, l := range links {
		authorIDs[i], roles[i], ords[i] = int64(l.AuthorID), l.Role, int32(l.Ordinal)
	}
	if _, err := r.db.Exec(ctx, `INSERT INTO book_authors (book_id, author_id, role, ordinal)
SELECT $1, * FROM unnest($2::bigint[], $3::text[], $4::smallint[])`, int64(bookID), authorIDs, roles, ords); err != nil {
		return mapErr(err)
	}
	_, err = r.db.Exec(ctx, `INSERT INTO author_books (author_id, book_id, role, ordinal)
SELECT a, $1, r, o FROM unnest($2::bigint[], $3::text[], $4::smallint[]) AS t(a, r, o)`, int64(bookID), authorIDs, roles, ords)
	return mapErr(err)
}
