package service

import (
	"context"
	"strconv"

	"github.com/example/worldlib/internal/cursor"
	"github.com/example/worldlib/internal/domain"
	"github.com/example/worldlib/internal/repository"
)

// ListBooks implements GET /books.
func (s *Service) ListBooks(ctx context.Context, f domain.BookFilter, lp domain.ListParams) (domain.Page[domain.Book], error) {
	r := s.store.Repo()
	items, more, err := r.ListBooks(ctx, f, lp)
	if err != nil {
		return domain.Page[domain.Book]{}, err
	}
	if err := r.LoadBookAuthors(ctx, items); err != nil {
		return domain.Page[domain.Book]{}, err
	}
	p := page(items, more, lp.Limit, func(b domain.Book) string {
		return cursor.Encode(cursor.Cursor{Sort: lp.Sort.String(), Value: repository.BookSortValue(b, lp.Sort.Field), ID: int64(b.ID)})
	})
	if lp.IncludeTotal {
		n, err := r.EstimateBooks(ctx, f)
		if err != nil {
			return p, err
		}
		p.Total, p.TotalIsEstimate = &n, true
	}
	return p, nil
}

// GetBook implements GET /books/{id}.
func (s *Service) GetBook(ctx context.Context, id domain.ID) (domain.Book, error) {
	r := s.store.Repo()
	b, err := r.GetBook(ctx, id, false)
	if err != nil {
		return b, err
	}
	bs := []domain.Book{b}
	if err := r.LoadBookAuthors(ctx, bs); err != nil {
		return b, err
	}
	return bs[0], nil
}

func checkAuthors(ctx context.Context, r *repository.Repo, links *[]domain.AuthorLink) error {
	if links == nil || len(*links) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(*links))
	for _, l := range *links {
		ids = append(ids, int64(l.AuthorID))
	}
	missing, err := r.MissingAuthors(ctx, ids)
	if err != nil {
		return err
	}
	if len(missing) > 0 {
		ve := &domain.ValidationError{Kind: domain.KindUnprocessable}
		for _, m := range missing {
			ve.Add("authors", "author "+strconv.FormatInt(m, 10)+" does not exist")
		}
		return ve
	}
	return nil
}

func linksSnapshot(links []domain.AuthorLink) []domain.AuthorLink {
	if links == nil {
		return []domain.AuthorLink{}
	}
	return links
}

func refsToLinks(refs []domain.BookAuthorRef) []domain.AuthorLink {
	out := make([]domain.AuthorLink, 0, len(refs))
	for _, r := range refs {
		out = append(out, domain.AuthorLink{AuthorID: r.AuthorID, Role: r.Role, Ordinal: r.Ordinal})
	}
	return out
}

// CreateBook implements POST /books.
func (s *Service) CreateBook(ctx context.Context, by domain.ID, in domain.BookInput) (domain.Book, error) {
	var b domain.Book
	links := domain.ApplyBook(&b, in, domain.ModeCreate)
	if err := domain.ValidateBook(&b, links, s.now()); err != nil {
		return b, err
	}
	b.ID = domain.ID(s.ids.Next())
	b.CreatedBy = by
	err := s.store.InTx(ctx, func(r *repository.Repo) error {
		if err := checkAuthors(ctx, r, links); err != nil {
			return err
		}
		if err := r.InsertBook(ctx, &b); err != nil {
			return err
		}
		if b.ISBN13 != nil {
			if err := r.ClaimISBN(ctx, *b.ISBN13, b.ID); err != nil {
				return err
			}
		}
		if err := r.ReplaceBookLinks(ctx, b.ID, *links); err != nil {
			return err
		}
		diff := CreateDiff(b)
		diff["authors"] = change{Old: nil, New: linksSnapshot(*links)}
		return s.audit(ctx, r, "book", b.ID, by, "create", diff)
	})
	if err != nil {
		return b, err
	}
	return s.GetBook(ctx, b.ID)
}

// UpdateBook implements PUT (ModeReplace) and PATCH (ModePatch).
func (s *Service) UpdateBook(ctx context.Context, by, id domain.ID, in domain.BookInput, mode domain.Mode, ifMatch *int) (domain.Book, error) {
	expected, err := ResolveVersion(ifMatch, in.Version)
	if err != nil {
		return domain.Book{}, err
	}
	err = s.store.InTx(ctx, func(r *repository.Repo) error {
		old, err := r.GetBook(ctx, id, true)
		if err != nil {
			return err
		}
		if old.Version != expected {
			return domain.ErrVersionConflict
		}
		olds := []domain.Book{old}
		if err := r.LoadBookAuthors(ctx, olds); err != nil {
			return err
		}
		old = olds[0]
		nb := old
		nb.Authors = nil
		links := domain.ApplyBook(&nb, in, mode)
		if err := domain.ValidateBook(&nb, links, s.now()); err != nil {
			return err
		}
		if err := checkAuthors(ctx, r, links); err != nil {
			return err
		}
		if err := r.UpdateBook(ctx, &nb, expected, by); err != nil {
			return err
		}
		if !eqPtr(old.ISBN13, nb.ISBN13) {
			if old.ISBN13 != nil {
				if err := r.ReleaseISBN(ctx, *old.ISBN13, id); err != nil {
					return err
				}
			}
			if nb.ISBN13 != nil {
				if err := r.ClaimISBN(ctx, *nb.ISBN13, id); err != nil {
					return err
				}
			}
		}
		oldCmp := old
		oldCmp.Authors = nil
		diff := Diff(oldCmp, nb)
		if links != nil {
			if err := r.ReplaceBookLinks(ctx, id, *links); err != nil {
				return err
			}
			ol := refsToLinks(old.Authors)
			if !equalLinks(ol, *links) {
				diff["authors"] = change{Old: ol, New: linksSnapshot(*links)}
			}
		}
		return s.audit(ctx, r, "book", id, by, "update", diff)
	})
	if err != nil {
		return domain.Book{}, err
	}
	return s.GetBook(ctx, id)
}

func equalLinks(a, b []domain.AuthorLink) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func eqPtr[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// DeleteBook soft-deletes a book. If-Match is optional for deletes.
func (s *Service) DeleteBook(ctx context.Context, by, id domain.ID, ifMatch *int) error {
	return s.store.InTx(ctx, func(r *repository.Repo) error {
		old, err := r.GetBook(ctx, id, true)
		if err != nil {
			return err
		}
		if ifMatch != nil && *ifMatch != old.Version {
			return domain.ErrVersionConflict
		}
		at, err := r.SoftDeleteBook(ctx, id, by)
		if err != nil {
			return err
		}
		if old.ISBN13 != nil {
			if err := r.ReleaseISBN(ctx, *old.ISBN13, id); err != nil {
				return err
			}
		}
		return s.audit(ctx, r, "book", id, by, "delete", map[string]change{"deleted_at": {Old: nil, New: at}})
	})
}
