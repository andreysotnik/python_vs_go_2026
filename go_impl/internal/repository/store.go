// Package repository implements PostgreSQL data access (pgx + squirrel).
// All list queries are keyset-paginated and bounded by LIMIT; no OFFSET is ever generated.
package repository

import (
	"context"
	"errors"
	"fmt"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/example/worldlib/internal/domain"
)

// DBTX is satisfied by *pgxpool.Pool and pgx.Tx.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Store owns the pool and creates repositories / transactions.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore wraps a pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Repo returns a non-transactional repository.
func (s *Store) Repo() *Repo { return &Repo{db: s.pool} }

// InTx runs fn inside a READ COMMITTED transaction (PgBouncer transaction-mode safe).
func (s *Store) InTx(ctx context.Context, fn func(r *Repo) error) error {
	return pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return fn(&Repo{db: tx})
	})
}

// Ping checks DB availability.
func (s *Store) Ping(ctx context.Context) error {
	var one int
	return s.pool.QueryRow(ctx, "SELECT 1").Scan(&one)
}

// Repo groups queries over a DBTX.
type Repo struct {
	db DBTX
}

var psql = sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

// mapErr converts pg errors to domain errors.
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		switch pe.Code {
		case "23505":
			return fmt.Errorf("%w: %s", domain.ErrDuplicate, pe.ConstraintName)
		case "23503":
			return domain.Unprocessable("authors", "references a non-existent entity ("+pe.ConstraintName+")")
		case "23514", "22001", "22003", "22007", "22008":
			return domain.Unprocessable(pe.ColumnName, pe.Message)
		}
	}
	return err
}

func (r *Repo) queryRows(ctx context.Context, b sq.Sqlizer) (pgx.Rows, error) {
	q, args, err := b.ToSql()
	if err != nil {
		return nil, err
	}
	return r.db.Query(ctx, q, args...)
}
