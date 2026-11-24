// Command library is the single binary of the World Library backend:
//
//	library serve        HTTP API
//	library migrate      apply SQL migrations (goose)
//	library seed         generate and bulk-load mock data (COPY)
//	library explain      EXPLAIN ANALYZE key queries, fail on Seq Scan; Markdown to stdout
//	library healthcheck  probe /healthz (for Docker HEALTHCHECK in distroless images)
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/example/worldlib/internal/config"
	"github.com/example/worldlib/internal/dbx"
	"github.com/example/worldlib/internal/explain"
	"github.com/example/worldlib/internal/httpapi"
	"github.com/example/worldlib/internal/idgen"
	dbmigrate "github.com/example/worldlib/internal/migrate"
	"github.com/example/worldlib/internal/repository"
	"github.com/example/worldlib/internal/seed"
	"github.com/example/worldlib/internal/service"
)

func main() { os.Exit(run()) }

func run() int {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	cfg, err := config.Load(nil)
	log := newLogger(cfg.LogLevel)
	if err != nil {
		log.Error("config", "error", err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch cmd {
	case "serve":
		err = serve(ctx, cfg, log)
	case "migrate":
		err = migrate(ctx, cfg, log)
	case "seed":
		err = runSeed(ctx, cfg, log)
	case "explain":
		err = runExplain(ctx, cfg, log)
	case "healthcheck":
		err = healthcheck(cfg)
	default:
		err = fmt.Errorf("unknown command %q (serve|migrate|seed|explain|healthcheck)", cmd)
	}
	if err != nil {
		log.Error(cmd+" failed", "error", err)
		return 1
	}
	return 0
}

func newLogger(level string) *slog.Logger {
	var l slog.Level
	_ = l.UnmarshalText([]byte(strings.ToUpper(level)))
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: l}))
}

func serve(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	pool, err := dbx.NewPool(ctx, cfg, "library-api")
	if err != nil {
		return err
	}
	defer pool.Close()
	ids, err := idgen.New(cfg.NodeID)
	if err != nil {
		return err
	}
	svc := service.New(repository.NewStore(pool), ids)
	m := httpapi.NewMetrics()
	m.RegisterRuntime(pool)
	api := httpapi.New(svc, log, m, httpapi.Options{RequestTimeout: cfg.RequestTimeout, AccessLog: true})
	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           api.Router(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      cfg.RequestTimeout + 5*time.Second,
		IdleTimeout:       90 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		log.Info("http server listening", "addr", cfg.HTTPAddr)
		errCh <- srv.ListenAndServe()
	}()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func migrate(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	return dbmigrate.Up(ctx, db, log)
}

func runSeed(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	cfg.DBMaxConns = int32(cfg.SeedWorkers + 4)
	cfg.StatementTimeout = 0
	pool, err := dbx.NewPool(ctx, cfg, "library-seed")
	if err != nil {
		return err
	}
	defer pool.Close()
	log.Info("seed started", "books", cfg.SeedBooks, "authors", cfg.SeedAuthors, "users", cfg.SeedUsers,
		"random", cfg.SeedRandom, "workers", cfg.SeedWorkers, "batch", cfg.SeedBatch)
	st, err := seed.Run(ctx, pool, log, seed.Options{Books: cfg.SeedBooks, Authors: cfg.SeedAuthors, Users: cfg.SeedUsers,
		Random: cfg.SeedRandom, Workers: cfg.SeedWorkers, Batch: cfg.SeedBatch})
	if err != nil {
		return err
	}
	log.Info("seed finished", "rows", st.Rows, "copy_seconds", st.Load.Seconds(), "copy_rows_per_s", int64(st.RowsPerS),
		"index_fk_seconds", st.Indexes.Seconds(), "vacuum_analyze_seconds", st.Analyze.Seconds(), "total_seconds", st.Total.Seconds())
	return nil
}

func runExplain(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	cfg.StatementTimeout = 0
	pool, err := dbx.NewPool(ctx, cfg, "library-explain")
	if err != nil {
		return err
	}
	defer pool.Close()
	var books, authors int64
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM books").Scan(&books)
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM authors").Scan(&authors)
	res, err := explain.Run(ctx, pool)
	if err != nil {
		return err
	}
	explain.Report(os.Stdout, res, fmt.Sprintf("%d books, %d authors", books, authors))
	bad := 0
	for _, r := range res {
		if !r.OK() {
			bad++
			log.Error("query plan check failed", "query", r.Name, "seq_scans", r.SeqScans, "offset", r.Offset)
		}
	}
	log.Info("explain finished", "queries", len(res), "failed", bad)
	if bad > 0 {
		return fmt.Errorf("%d queries use Seq Scan on large tables or OFFSET", bad)
	}
	return nil
}

func healthcheck(cfg config.Config) error {
	addr := cfg.HTTPAddr
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	c := http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+addr+"/healthz", http.NoBody)
	if err != nil {
		return err
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}
