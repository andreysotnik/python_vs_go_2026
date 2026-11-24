// Package migrate applies the embedded goose migrations.
package migrate

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/pressly/goose/v3"

	"github.com/example/worldlib/migrations"
)

// Up applies all migrations and makes sure future audit partitions exist.
func Up(ctx context.Context, db *sql.DB, log *slog.Logger) error {
	var lastErr error
	for i := 0; i < 30; i++ { // wait for the database to come up
		if lastErr = db.PingContext(ctx); lastErr == nil {
			break
		}
		time.Sleep(time.Second)
	}
	if lastErr != nil {
		return lastErr
	}
	p, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		return err
	}
	res, err := p.Up(ctx)
	if err != nil {
		return err
	}
	for _, r := range res {
		log.Info("migration applied", "version", r.Source.Version, "file", r.Source.Path, "duration_ms", r.Duration.Milliseconds())
	}
	if _, err := db.ExecContext(ctx, "SELECT ensure_audit_partitions(date_trunc('month', now())::date, 13)"); err != nil {
		return err
	}
	v, _ := p.GetDBVersion(ctx)
	log.Info("database is up to date", "version", v)
	return nil
}
