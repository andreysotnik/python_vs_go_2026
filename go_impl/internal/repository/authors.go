package repository

import (
	"context"
	"errors"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"

	"github.com/example/worldlib/internal/cursor"
	"github.com/example/worldlib/internal/domain"
)

const authorCols = `a.id, a.full_name, a.original_name, a.birth_date, a.death_date, a.country_code, a.biography,
 a.external_ids, a.created_at, a.created_by, a.updated_at, a.updated_by, a.version, a.deleted_at`

func scanAuthor(row pgx.Row) (domain.Author, error) {
	var a domain.Author
	var birth, death *time.Time
	err := row.Scan(&a.ID, &a.FullName, &a.OriginalName, &birth, &death, &a.CountryCode, &a.Biography,
		&a.ExternalIDs, &a.CreatedAt, &a.CreatedBy, &a.UpdatedAt, &a.UpdatedBy, &a.Version, &a.DeletedAt)
	if birth != nil {
		a.BirthDate = &domain.Date{Time: *birth}
	}
	if death != nil {
		a.DeathDate = &domain.Date{Time: *death}
	}
	if a.ExternalIDs == nil {
		a.ExternalIDs = map[string]string{}
	}
	return a, err
}

func dateArg(d *domain.Date) any {
	if d == nil {
		return nil
	}
	return d.Time
}

// AuthorWhere converts a filter into predicates.
func AuthorWhere(f domain.AuthorFilter) sq.And {
	w := sq.And{sq.Expr("a.deleted_at IS NULL")}
	if f.Name != nil {
		w = append(w, sq.Expr(AuthorNormExpr+` LIKE '%' || norm_text(?) || '%'`, escapeLike(*f.Name)))
	}
	if f.Country != nil {
		w = append(w, sq.Expr("a.country_code = ?", *f.Country))
	}
	if f.BornFrom != nil {
		w = append(w, sq.Expr("a.birth_date >= ?", f.BornFrom.Time))
	}
	if f.BornTo != nil {
		w = append(w, sq.Expr("a.birth_date <= ?", f.BornTo.Time))
	}
	if f.Q != nil {
		w = append(w, sq.Expr("a.search_vector @@ websearch_to_tsquery("+tsConfig+", ?)", *f.Q))
	}
	return w
}

// BuildListAuthors returns the keyset plan for GET /authors.
func BuildListAuthors(f domain.AuthorFilter, p domain.ListParams) (KeysetPlan, error) {
	base := psql.Select(authorCols).From("authors a").Where(AuthorWhere(f))
	return buildKeyset(base, authorSortCols[p.Sort.Field], "a.id", p.Sort.Desc, p.Cursor, p.Limit)
}

// AuthorSortValue extracts the cursor value of an author for a sort field.
func AuthorSortValue(a domain.Author, field string) *string {
	switch field {
	case "full_name":
		return FormatSortValue(a.FullName)
	case "birth_date":
		return FormatSortValue(a.BirthDate)
	default:
		return FormatSortValue(a.CreatedAt)
	}
}

// ListAuthors runs GET /authors.
func (r *Repo) ListAuthors(ctx context.Context, f domain.AuthorFilter, p domain.ListParams) ([]domain.Author, bool, error) {
	plan, err := BuildListAuthors(f, p)
	if err != nil {
		return nil, false, err
	}
	return runKeyset(ctx, r.db, plan, func(rows pgx.Rows) (domain.Author, error) { return scanAuthor(rows) })
}

// EstimateAuthors returns the planner estimate of matching authors.
func (r *Repo) EstimateAuthors(ctx context.Context, f domain.AuthorFilter) (int64, error) {
	return estimateRows(ctx, r.db, psql.Select("1").From("authors a").Where(AuthorWhere(f)))
}

// GetAuthor loads a non-deleted author.
func (r *Repo) GetAuthor(ctx context.Context, id domain.ID, forUpdate bool) (domain.Author, error) {
	q := "SELECT " + authorCols + " FROM authors a WHERE a.id = $1 AND a.deleted_at IS NULL"
	if forUpdate {
		q += " FOR UPDATE"
	}
	a, err := scanAuthor(r.db.QueryRow(ctx, q, int64(id)))
	return a, mapErr(err)
}

// AuthorExists reports whether an author row exists (including soft-deleted).
func (r *Repo) AuthorExists(ctx context.Context, id domain.ID) (bool, error) {
	var ok bool
	err := r.db.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM authors WHERE id = $1)", int64(id)).Scan(&ok)
	return ok, mapErr(err)
}

// InsertAuthor inserts an author.
func (r *Repo) InsertAuthor(ctx context.Context, a *domain.Author) error {
	err := r.db.QueryRow(ctx, `INSERT INTO authors (id, full_name, original_name, birth_date, death_date, country_code,
 biography, external_ids, created_by, updated_by)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9) RETURNING created_at, updated_at, version`,
		int64(a.ID), a.FullName, a.OriginalName, dateArg(a.BirthDate), dateArg(a.DeathDate), a.CountryCode,
		a.Biography, a.ExternalIDs, int64(a.CreatedBy)).Scan(&a.CreatedAt, &a.UpdatedAt, &a.Version)
	if err != nil {
		return mapErr(err)
	}
	a.UpdatedBy = a.CreatedBy
	return nil
}

// UpdateAuthor writes all mutable columns and bumps the version.
func (r *Repo) UpdateAuthor(ctx context.Context, a *domain.Author, expectedVersion int, by domain.ID) error {
	err := r.db.QueryRow(ctx, `UPDATE authors SET full_name=$2, original_name=$3, birth_date=$4, death_date=$5,
 country_code=$6, biography=$7, external_ids=$8, updated_by=$9, updated_at=now(), version=version+1
WHERE id=$1 AND version=$10 AND deleted_at IS NULL RETURNING updated_at, updated_by, version`,
		int64(a.ID), a.FullName, a.OriginalName, dateArg(a.BirthDate), dateArg(a.DeathDate), a.CountryCode,
		a.Biography, a.ExternalIDs, int64(by), expectedVersion).Scan(&a.UpdatedAt, &a.UpdatedBy, &a.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrVersionConflict
	}
	return mapErr(err)
}

// SoftDeleteAuthor marks the author deleted.
func (r *Repo) SoftDeleteAuthor(ctx context.Context, id domain.ID, by domain.ID) (time.Time, error) {
	var at time.Time
	err := r.db.QueryRow(ctx, `UPDATE authors SET deleted_at=now(), updated_at=now(), updated_by=$2, version=version+1
WHERE id=$1 AND deleted_at IS NULL RETURNING deleted_at`, int64(id), int64(by)).Scan(&at)
	return at, mapErr(err)
}

// BuildAuthorBooks returns the keyset query for GET /authors/{id}/books ordered by (book_id, role),
// served by the author_books primary key (author_id, book_id, role) of a single partition.
func BuildAuthorBooks(authorID domain.ID, cur *cursor.Cursor, limit int) sq.SelectBuilder {
	b := psql.Select(bookCols+", ab.role, ab.ordinal").
		From("author_books ab").
		Join("books b ON b.id = ab.book_id AND b.deleted_at IS NULL").
		Where("ab.author_id = ?", int64(authorID))
	if cur != nil && cur.Value != nil {
		b = b.Where("(ab.book_id, ab.role) > (?, ?)", cur.ID, *cur.Value)
	}
	return b.OrderBy("ab.book_id", "ab.role").Limit(uint64(limit + 1))
}

// AuthorBooks lists books of an author.
func (r *Repo) AuthorBooks(ctx context.Context, authorID domain.ID, cur *cursor.Cursor, limit int) ([]domain.AuthorBook, bool, error) {
	rows, err := r.queryRows(ctx, BuildAuthorBooks(authorID, cur, limit))
	if err != nil {
		return nil, false, mapErr(err)
	}
	defer rows.Close()
	out := make([]domain.AuthorBook, 0, limit+1)
	for rows.Next() {
		var ab domain.AuthorBook
		b, err := scanBook(rows, &ab.Role, &ab.Ordinal)
		if err != nil {
			return nil, false, err
		}
		ab.Book = b
		out = append(out, ab)
	}
	if err := rows.Err(); err != nil {
		return nil, false, mapErr(err)
	}
	more := len(out) > limit
	if more {
		out = out[:limit]
	}
	return out, more, nil
}
