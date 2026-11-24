package repository

import (
	"context"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/example/worldlib/internal/cursor"
	"github.com/example/worldlib/internal/domain"
)

// Relevance = ts_rank (full-text) + word_similarity (trigram, typo tolerant).
// Candidates: FTS match OR trigram word-similarity match, both GIN-indexed (BitmapOr).
const searchBooksSQL = `SELECT 'book'::text AS type, b.id, b.title AS label, b.original_title AS original,
  (ts_rank(b.search_vector, websearch_to_tsquery('simple_unaccent', $1))
   + word_similarity(norm_text($1), ` + BookNormExpr + `))::float8 AS rank
FROM books b
WHERE b.deleted_at IS NULL
  AND (b.search_vector @@ websearch_to_tsquery('simple_unaccent', $1) OR norm_text($1) <% ` + BookNormExpr + `)`

const searchAuthorsSQL = `SELECT 'author'::text AS type, a.id, a.full_name AS label, a.original_name AS original,
  (ts_rank(a.search_vector, websearch_to_tsquery('simple_unaccent', $1))
   + word_similarity(norm_text($1), ` + AuthorNormExpr + `))::float8 AS rank
FROM authors a
WHERE a.deleted_at IS NULL
  AND (a.search_vector @@ websearch_to_tsquery('simple_unaccent', $1) OR norm_text($1) <% ` + AuthorNormExpr + `)`

// BuildSearch renders the search query; keyset on (rank DESC, id DESC).
func BuildSearch(p domain.SearchParams) (string, []any, error) {
	var inner string
	switch p.Type {
	case "books":
		inner = searchBooksSQL
	case "authors":
		inner = searchAuthorsSQL
	default:
		inner = searchBooksSQL + "\nUNION ALL\n" + searchAuthorsSQL
	}
	args := []any{p.Q}
	where := ""
	if p.Cursor != nil {
		if p.Cursor.Value == nil {
			return "", nil, domain.BadRequest("cursor", "malformed cursor value")
		}
		rank, err := strconv.ParseFloat(*p.Cursor.Value, 64)
		if err != nil {
			return "", nil, domain.BadRequest("cursor", "malformed cursor value")
		}
		args = append(args, rank, p.Cursor.ID)
		where = "WHERE (s.rank, s.id) < ($2::float8, $3::bigint)"
	}
	args = append(args, p.Limit+1)
	q := "SELECT s.type, s.id, s.label, s.original, s.rank FROM (\n" + inner + "\n) s " + where +
		"\nORDER BY s.rank DESC, s.id DESC LIMIT $" + strconv.Itoa(len(args))
	return q, args, nil
}

// Search runs the global relevance search.
func (r *Repo) Search(ctx context.Context, p domain.SearchParams) ([]domain.SearchHit, bool, error) {
	q, args, err := BuildSearch(p)
	if err != nil {
		return nil, false, err
	}
	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, false, mapErr(err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.SearchHit, error) {
		var h domain.SearchHit
		err := row.Scan(&h.Type, &h.ID, &h.Label, &h.Original, &h.Rank)
		return h, err
	})
	if err != nil {
		return nil, false, mapErr(err)
	}
	more := len(out) > p.Limit
	if more {
		out = out[:p.Limit]
	}
	return out, more, nil
}

// SearchCursor builds the next cursor for search results.
func SearchCursor(p domain.SearchParams, last domain.SearchHit) string {
	return cursor.Encode(cursor.Cursor{Sort: "rank:" + p.Type, Value: FormatSortValue(last.Rank), ID: int64(last.ID)})
}
