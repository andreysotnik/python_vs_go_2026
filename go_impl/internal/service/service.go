// Package service implements use cases: validation, optimistic locking, auditing and
// transaction orchestration over the repository.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"time"

	"github.com/example/worldlib/internal/cursor"
	"github.com/example/worldlib/internal/domain"
	"github.com/example/worldlib/internal/idgen"
	"github.com/example/worldlib/internal/repository"
)

// Service is the application facade used by HTTP handlers.
type Service struct {
	store *repository.Store
	ids   *idgen.Generator
	now   func() time.Time
}

// New creates a service.
func New(store *repository.Store, ids *idgen.Generator) *Service {
	return &Service{store: store, ids: ids, now: time.Now}
}

// Ready checks the database.
func (s *Service) Ready(ctx context.Context) error { return s.store.Ping(ctx) }

// ResolveVersion picks the expected version from If-Match (preferred) or the body.
func ResolveVersion(ifMatch *int, body domain.Opt[int]) (int, error) {
	switch {
	case ifMatch != nil:
		return *ifMatch, nil
	case body.Set && !body.Null:
		return body.Value, nil
	}
	return 0, domain.ErrPreconditionRequired
}

func page[T any](items []T, more bool, limit int, next func(T) string) domain.Page[T] {
	p := domain.Page[T]{Items: items, Limit: limit}
	if p.Items == nil {
		p.Items = []T{}
	}
	if more && len(items) > 0 {
		c := next(items[len(items)-1])
		p.NextCursor = &c
	}
	return p
}

// snapshot converts an entity to a comparable map (JSON field names), excluding audit fields.
func snapshot(v any) map[string]any {
	b, _ := json.Marshal(v)
	m := map[string]any{}
	_ = json.Unmarshal(b, &m)
	for _, k := range []string{"id", "created_at", "created_by", "updated_at", "updated_by", "version"} {
		delete(m, k)
	}
	return m
}

type change struct {
	Old any `json:"old"`
	New any `json:"new"`
}

// Diff returns {"field": {"old":..,"new":..}} for changed fields.
func Diff(oldV, newV any) map[string]change {
	o, n := snapshot(oldV), snapshot(newV)
	out := map[string]change{}
	keys := make([]string, 0, len(n))
	for k := range n {
		keys = append(keys, k)
	}
	for k := range o {
		if _, ok := n[k]; !ok {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	for _, k := range keys {
		if !reflect.DeepEqual(o[k], n[k]) {
			out[k] = change{Old: o[k], New: n[k]}
		}
	}
	return out
}

// CreateDiff returns {"field": {"old": null, "new": v}} for every non-empty field of a new entity.
func CreateDiff(newV any) map[string]change {
	out := map[string]change{}
	for k, v := range snapshot(newV) {
		switch x := v.(type) {
		case nil:
			continue
		case string:
			if x == "" {
				continue
			}
		case []any:
			if len(x) == 0 {
				continue
			}
		case map[string]any:
			if len(x) == 0 {
				continue
			}
		}
		out[k] = change{Old: nil, New: v}
	}
	return out
}

func (s *Service) audit(ctx context.Context, r *repository.Repo, entity string, id, by domain.ID, action string, diff any) error {
	raw, err := json.Marshal(diff)
	if err != nil {
		return err
	}
	e := domain.AuditEntry{ID: domain.ID(s.ids.Next()), EntityType: entity, EntityID: id, Action: action, ChangedBy: by, Diff: raw}
	return r.InsertAudit(ctx, &e)
}

// History returns the audit trail of a book or author (also for soft-deleted entities).
func (s *Service) History(ctx context.Context, entity string, id domain.ID, lp domain.ListParams) (domain.Page[domain.AuditEntry], error) {
	r := s.store.Repo()
	var exists bool
	var err error
	if entity == "book" {
		exists, err = r.BookExists(ctx, id)
	} else {
		exists, err = r.AuthorExists(ctx, id)
	}
	if err != nil {
		return domain.Page[domain.AuditEntry]{}, err
	}
	if !exists {
		return domain.Page[domain.AuditEntry]{}, domain.ErrNotFound
	}
	items, more, err := r.History(ctx, entity, id, lp.Cursor, lp.Limit)
	if err != nil {
		return domain.Page[domain.AuditEntry]{}, err
	}
	return page(items, more, lp.Limit, func(e domain.AuditEntry) string {
		return cursor.Encode(cursor.Cursor{Sort: "-changed_at", Value: repository.FormatSortValue(e.ChangedAt), ID: int64(e.ID)})
	}), nil
}

// Search runs global search.
func (s *Service) Search(ctx context.Context, p domain.SearchParams) (domain.Page[domain.SearchHit], error) {
	items, more, err := s.store.Repo().Search(ctx, p)
	if err != nil {
		return domain.Page[domain.SearchHit]{}, err
	}
	return page(items, more, p.Limit, func(h domain.SearchHit) string { return repository.SearchCursor(p, h) }), nil
}

// CreateUser registers a user.
func (s *Service) CreateUser(ctx context.Context, in domain.UserInput) (domain.User, error) {
	if err := domain.ValidateUser(&in); err != nil {
		return domain.User{}, err
	}
	u := domain.User{ID: domain.ID(s.ids.Next()), Username: in.Username, Email: in.Email}
	if err := s.store.Repo().InsertUser(ctx, &u); err != nil {
		return domain.User{}, err
	}
	return u, nil
}

// GetUser returns a user.
func (s *Service) GetUser(ctx context.Context, id domain.ID) (domain.User, error) {
	return s.store.Repo().GetUser(ctx, id)
}

// Authenticate resolves the acting user id (used by the auth middleware).
func (s *Service) Authenticate(ctx context.Context, raw string) (domain.ID, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("%w: X-User-Id must be a positive integer", domain.ErrUnauthenticated)
	}
	if _, err := s.store.Repo().GetUser(ctx, domain.ID(id)); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return 0, fmt.Errorf("%w: unknown user %d", domain.ErrUnauthenticated, id)
		}
		return 0, err
	}
	return domain.ID(id), nil
}
