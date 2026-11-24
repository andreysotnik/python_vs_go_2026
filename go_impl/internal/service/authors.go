package service

import (
	"context"

	"github.com/example/worldlib/internal/cursor"
	"github.com/example/worldlib/internal/domain"
	"github.com/example/worldlib/internal/repository"
)

// ListAuthors implements GET /authors.
func (s *Service) ListAuthors(ctx context.Context, f domain.AuthorFilter, lp domain.ListParams) (domain.Page[domain.Author], error) {
	r := s.store.Repo()
	items, more, err := r.ListAuthors(ctx, f, lp)
	if err != nil {
		return domain.Page[domain.Author]{}, err
	}
	p := page(items, more, lp.Limit, func(a domain.Author) string {
		return cursor.Encode(cursor.Cursor{Sort: lp.Sort.String(), Value: repository.AuthorSortValue(a, lp.Sort.Field), ID: int64(a.ID)})
	})
	if lp.IncludeTotal {
		n, err := r.EstimateAuthors(ctx, f)
		if err != nil {
			return p, err
		}
		p.Total, p.TotalIsEstimate = &n, true
	}
	return p, nil
}

// GetAuthor implements GET /authors/{id}.
func (s *Service) GetAuthor(ctx context.Context, id domain.ID) (domain.Author, error) {
	return s.store.Repo().GetAuthor(ctx, id, false)
}

// AuthorBooks implements GET /authors/{id}/books.
func (s *Service) AuthorBooks(ctx context.Context, id domain.ID, lp domain.ListParams) (domain.Page[domain.AuthorBook], error) {
	r := s.store.Repo()
	if _, err := r.GetAuthor(ctx, id, false); err != nil {
		return domain.Page[domain.AuthorBook]{}, err
	}
	items, more, err := r.AuthorBooks(ctx, id, lp.Cursor, lp.Limit)
	if err != nil {
		return domain.Page[domain.AuthorBook]{}, err
	}
	books := make([]domain.Book, len(items))
	for i := range items {
		books[i] = items[i].Book
	}
	if err := r.LoadBookAuthors(ctx, books); err != nil {
		return domain.Page[domain.AuthorBook]{}, err
	}
	for i := range items {
		items[i].Book = books[i]
	}
	return page(items, more, lp.Limit, func(b domain.AuthorBook) string {
		role := b.Role
		return cursor.Encode(cursor.Cursor{Sort: "book_id", Value: &role, ID: int64(b.ID)})
	}), nil
}

// CreateAuthor implements POST /authors.
func (s *Service) CreateAuthor(ctx context.Context, by domain.ID, in domain.AuthorInput) (domain.Author, error) {
	var a domain.Author
	domain.ApplyAuthor(&a, in, domain.ModeCreate)
	if err := domain.ValidateAuthor(&a, s.now()); err != nil {
		return a, err
	}
	a.ID = domain.ID(s.ids.Next())
	a.CreatedBy = by
	err := s.store.InTx(ctx, func(r *repository.Repo) error {
		if err := r.InsertAuthor(ctx, &a); err != nil {
			return err
		}
		return s.audit(ctx, r, "author", a.ID, by, "create", CreateDiff(a))
	})
	return a, err
}

// UpdateAuthor implements PUT/PATCH /authors/{id}.
func (s *Service) UpdateAuthor(ctx context.Context, by, id domain.ID, in domain.AuthorInput, mode domain.Mode, ifMatch *int) (domain.Author, error) {
	expected, err := ResolveVersion(ifMatch, in.Version)
	if err != nil {
		return domain.Author{}, err
	}
	var out domain.Author
	err = s.store.InTx(ctx, func(r *repository.Repo) error {
		old, err := r.GetAuthor(ctx, id, true)
		if err != nil {
			return err
		}
		if old.Version != expected {
			return domain.ErrVersionConflict
		}
		na := old
		domain.ApplyAuthor(&na, in, mode)
		if err := domain.ValidateAuthor(&na, s.now()); err != nil {
			return err
		}
		if err := r.UpdateAuthor(ctx, &na, expected, by); err != nil {
			return err
		}
		out = na
		return s.audit(ctx, r, "author", id, by, "update", Diff(old, na))
	})
	return out, err
}

// DeleteAuthor soft-deletes an author.
func (s *Service) DeleteAuthor(ctx context.Context, by, id domain.ID, ifMatch *int) error {
	return s.store.InTx(ctx, func(r *repository.Repo) error {
		old, err := r.GetAuthor(ctx, id, true)
		if err != nil {
			return err
		}
		if ifMatch != nil && *ifMatch != old.Version {
			return domain.ErrVersionConflict
		}
		at, err := r.SoftDeleteAuthor(ctx, id, by)
		if err != nil {
			return err
		}
		return s.audit(ctx, r, "author", id, by, "delete", map[string]change{"deleted_at": {Old: nil, New: at}})
	})
}
