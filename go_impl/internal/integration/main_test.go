// Package integration runs API-level tests against a real PostgreSQL.
// Set TEST_DATABASE_URL to an admin connection string (database "postgres"); tests create and drop
// their own databases. Without it the tests are skipped.
package integration

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/example/worldlib/internal/config"
	"github.com/example/worldlib/internal/dbx"
	"github.com/example/worldlib/internal/httpapi"
	"github.com/example/worldlib/internal/idgen"
	dbmigrate "github.com/example/worldlib/internal/migrate"
	"github.com/example/worldlib/internal/repository"
	"github.com/example/worldlib/internal/service"
)

var (
	srv     *httptest.Server
	pool    *pgxpool.Pool
	adminDB string
)

const testDB = "library_it"

func dbURL(admin, name string) string {
	u, err := url.Parse(admin)
	if err != nil {
		panic(err)
	}
	u.Path = "/" + name
	return u.String()
}

// createDB (re)creates a database and applies migrations; returns its URL.
func createDB(ctx context.Context, admin, name string) (string, error) {
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		return "", err
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
		return "", err
	}
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		return "", err
	}
	u := dbURL(admin, name)
	db, err := sql.Open("pgx", u)
	if err != nil {
		return "", err
	}
	defer db.Close()
	if err := dbmigrate.Up(ctx, db, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		return "", err
	}
	return u, nil
}

func dropDB(ctx context.Context, admin, name string) {
	if conn, err := pgx.Connect(ctx, admin); err == nil {
		_, _ = conn.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
		_ = conn.Close(ctx)
	}
}

func TestMain(m *testing.M) {
	adminDB = os.Getenv("TEST_DATABASE_URL")
	if adminDB == "" {
		fmt.Println("TEST_DATABASE_URL not set: skipping integration tests")
		os.Exit(0)
	}
	ctx := context.Background()
	u, err := createDB(ctx, adminDB, testDB)
	if err != nil {
		fmt.Println("setup:", err)
		os.Exit(1)
	}
	cfg, _ := config.Load(func(k string) string {
		if k == "DATABASE_URL" {
			return u
		}
		return ""
	})
	pool, err = dbx.NewPool(ctx, cfg, "integration-tests")
	if err != nil {
		fmt.Println("pool:", err)
		os.Exit(1)
	}
	ids, _ := idgen.New(5)
	svc := service.New(repository.NewStore(pool), ids)
	metrics := httpapi.NewMetrics()
	metrics.RegisterRuntime(pool)
	api := httpapi.New(svc, slog.New(slog.NewJSONHandler(io.Discard, nil)), metrics, httpapi.Options{RequestTimeout: 10 * time.Second, AccessLog: true})
	srv = httptest.NewServer(api.Router())
	code := m.Run()
	srv.Close()
	pool.Close()
	dropDB(ctx, adminDB, testDB)
	os.Exit(code)
}

// ---- tiny HTTP client ----

type resp struct {
	Status int
	Header http.Header
	Body   []byte
}

func (r resp) json(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.Body, v); err != nil {
		t.Fatalf("decode %s: %v", r.Body, err)
	}
}

func (r resp) m(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	r.json(t, &m)
	return m
}

type hdr map[string]string

func call(t *testing.T, method, path string, body any, h hdr) resp {
	t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	default:
		buf, _ := json.Marshal(b)
		rd = bytes.NewReader(buf)
	}
	req, _ := http.NewRequest(method, srv.URL+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range h {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return resp{Status: res.StatusCode, Header: res.Header, Body: b}
}

func expect(t *testing.T, r resp, status int) resp {
	t.Helper()
	if r.Status != status {
		t.Fatalf("expected %d, got %d: %s", status, r.Status, r.Body)
	}
	if status >= 400 && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/problem+json") {
		t.Fatalf("error without problem+json content type: %s", r.Header.Get("Content-Type"))
	}
	return r
}

func as(user string) hdr { return hdr{"X-User-Id": user} }

func newUser(t *testing.T, name string) string {
	t.Helper()
	name = fmt.Sprintf("%s_%d", name, time.Now().UnixNano()%1_000_000_000)
	r := expect(t, call(t, "POST", "/api/v1/users", map[string]any{"username": name, "email": name + "@example.org"}, nil), 201)
	return r.m(t)["id"].(string)
}

type page struct {
	Items           []map[string]any `json:"items"`
	NextCursor      *string          `json:"next_cursor"`
	Limit           int              `json:"limit"`
	Total           *int64           `json:"total"`
	TotalIsEstimate bool             `json:"total_is_estimate"`
}

func getPage(t *testing.T, path string, q url.Values) page {
	t.Helper()
	r := expect(t, call(t, "GET", path+"?"+q.Encode(), nil, nil), 200)
	var p page
	r.json(t, &p)
	return p
}

// walk follows next_cursor until exhaustion and returns all items.
func walk(t *testing.T, path string, q url.Values) []map[string]any {
	t.Helper()
	var all []map[string]any
	q = cloneQ(q)
	for i := 0; i < 1000; i++ {
		p := getPage(t, path, q)
		all = append(all, p.Items...)
		if p.NextCursor == nil {
			return all
		}
		q.Set("cursor", *p.NextCursor)
	}
	t.Fatal("pagination did not terminate")
	return nil
}

func cloneQ(q url.Values) url.Values {
	out := url.Values{}
	for k, v := range q {
		out[k] = append([]string(nil), v...)
	}
	return out
}

func ids(items []map[string]any) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it["id"].(string)
	}
	return out
}

func qv(kv ...string) url.Values {
	q := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		q.Set(kv[i], kv[i+1])
	}
	return q
}
