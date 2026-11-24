// Package dbx builds the pgx connection pool.
package dbx

import (
	"context"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/example/worldlib/internal/config"
)

// NewPool creates a pool. In direct mode statement_timeout is sent as a startup parameter
// (no extra round trip). In PgBouncer transaction mode session state is not allowed, so the
// timeout must be configured on the role (ALTER ROLE ... SET statement_timeout) and the
// extended protocol runs without named prepared statements.
func NewPool(ctx context.Context, c config.Config, appName string) (*pgxpool.Pool, error) {
	pc, err := pgxpool.ParseConfig(c.DatabaseURL)
	if err != nil {
		return nil, err
	}
	pc.MaxConns = c.DBMaxConns
	pc.MinConns = c.DBMinConns
	pc.ConnConfig.RuntimeParams["application_name"] = appName
	if c.PgBouncer {
		pc.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeExec
	} else if c.StatementTimeout > 0 {
		pc.ConnConfig.RuntimeParams["statement_timeout"] = strconv.FormatInt(c.StatementTimeout.Milliseconds(), 10)
	}
	return pgxpool.NewWithConfig(ctx, pc)
}
