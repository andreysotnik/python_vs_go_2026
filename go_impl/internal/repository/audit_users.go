package repository

import (
	"context"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"

	"github.com/example/worldlib/internal/cursor"
	"github.com/example/worldlib/internal/domain"
)

// InsertAudit appends an audit record (same transaction as the change; changed_at = now()).
func (r *Repo) InsertAudit(ctx context.Context, e *domain.AuditEntry) error {
	err := r.db.QueryRow(ctx, `INSERT INTO audit_log (id, entity_type, entity_id, action, changed_by, diff)
VALUES ($1,$2,$3,$4,$5,$6) RETURNING changed_at`,
		int64(e.ID), e.EntityType, int64(e.EntityID), e.Action, int64(e.ChangedBy), e.Diff).Scan(&e.ChangedAt)
	return mapErr(err)
}

// BuildHistory returns the keyset query for an entity's history (newest first).
func BuildHistory(entityType string, id domain.ID, cur *cursor.Cursor, limit int) (sq.SelectBuilder, error) {
	b := psql.Select("id, entity_type, entity_id, action, changed_by, changed_at, diff").
		From("audit_log").
		Where("entity_type = ? AND entity_id = ?", entityType, int64(id))
	if cur != nil {
		if cur.Value == nil {
			return b, domain.BadRequest("cursor", "malformed cursor value")
		}
		t, err := time.Parse(time.RFC3339Nano, *cur.Value)
		if err != nil {
			return b, domain.BadRequest("cursor", "malformed cursor value")
		}
		b = b.Where("(changed_at, id) < (?, ?)", t, cur.ID)
	}
	return b.OrderBy("changed_at DESC", "id DESC").Limit(uint64(limit + 1)), nil
}

// History lists audit entries of an entity.
func (r *Repo) History(ctx context.Context, entityType string, id domain.ID, cur *cursor.Cursor, limit int) ([]domain.AuditEntry, bool, error) {
	b, err := BuildHistory(entityType, id, cur, limit)
	if err != nil {
		return nil, false, err
	}
	rows, err := r.queryRows(ctx, b)
	if err != nil {
		return nil, false, mapErr(err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.AuditEntry, error) {
		var e domain.AuditEntry
		var diff []byte
		err := row.Scan(&e.ID, &e.EntityType, &e.EntityID, &e.Action, &e.ChangedBy, &e.ChangedAt, &diff)
		e.Diff = diff
		return e, err
	})
	if err != nil {
		return nil, false, mapErr(err)
	}
	more := len(out) > limit
	if more {
		out = out[:limit]
	}
	return out, more, nil
}

// InsertUser creates a user.
func (r *Repo) InsertUser(ctx context.Context, u *domain.User) error {
	err := r.db.QueryRow(ctx, "INSERT INTO users (id, username, email) VALUES ($1,$2,$3) RETURNING created_at",
		int64(u.ID), u.Username, u.Email).Scan(&u.CreatedAt)
	return mapErr(err)
}

// GetUser loads a user.
func (r *Repo) GetUser(ctx context.Context, id domain.ID) (domain.User, error) {
	var u domain.User
	err := r.db.QueryRow(ctx, "SELECT id, username, email, created_at FROM users WHERE id = $1", int64(id)).
		Scan(&u.ID, &u.Username, &u.Email, &u.CreatedAt)
	return u, mapErr(err)
}
