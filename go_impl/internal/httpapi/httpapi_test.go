package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgconn"
	yaml "go.yaml.in/yaml/v3"

	"github.com/example/worldlib/api"
	"github.com/example/worldlib/internal/domain"
)

func TestProblemMapping(t *testing.T) {
	cases := []struct {
		err    error
		status int
	}{
		{domain.BadRequest("x", "y"), 400},
		{domain.Unprocessable("x", "y"), 422},
		{domain.ErrNotFound, 404},
		{fmt.Errorf("wrap: %w", domain.ErrVersionConflict), 409},
		{fmt.Errorf("%w: isbn", domain.ErrDuplicate), 409},
		{domain.ErrPreconditionRequired, 428},
		{domain.ErrUnauthenticated, 401},
		{context.DeadlineExceeded, 504},
		{&pgconn.PgError{Code: "57014"}, 504},
		{errors.New("boom"), 500},
	}
	for _, c := range cases {
		if p := problemFor(c.err); p.Status != c.status || p.Type == "" || p.Title == "" {
			t.Errorf("%v -> %+v", c.err, p)
		}
	}
}

func TestIfMatch(t *testing.T) {
	for h, want := range map[string]int{`"3"`: 3, `W/"4"`: 4, `5`: 5} {
		r := httptest.NewRequest("PUT", "/", nil)
		r.Header.Set("If-Match", h)
		v, err := ifMatch(r)
		if err != nil || *v != want {
			t.Fatalf("%s: %v", h, err)
		}
	}
	r := httptest.NewRequest("PUT", "/", nil)
	if v, err := ifMatch(r); v != nil || err != nil {
		t.Fatal("absent If-Match")
	}
	r.Header.Set("If-Match", "*")
	if _, err := ifMatch(r); err == nil {
		t.Fatal("* not supported")
	}
}

func TestDecode(t *testing.T) {
	type in struct {
		A int `json:"a"`
	}
	mk := func(body, ct string) *http.Request {
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		if ct != "" {
			r.Header.Set("Content-Type", ct)
		}
		return r
	}
	var v in
	if err := decode(mk(`{"a":1}`, "application/json; charset=utf-8"), &v); err != nil || v.A != 1 {
		t.Fatal(err)
	}
	if err := decode(mk(`{"a":1}`, ""), &v); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ body, ct string }{
		{`{"a":1}`, "text/plain"}, {``, ""}, {`{"a":"x"}`, ""}, {`{"b":1}`, ""}, {`{"a":1} {"a":2}`, ""}, {`{`, ""},
	} {
		if err := decode(mk(c.body, c.ct), &v); err == nil {
			t.Errorf("%q accepted", c.body)
		}
	}
}

// TestOpenAPIMatchesRouter makes sure every API route is documented and vice versa.
func TestOpenAPIMatchesRouter(t *testing.T) {
	var spec struct {
		OpenAPI string                    `yaml:"openapi"`
		Paths   map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(api.OpenAPI, &spec); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(spec.OpenAPI, "3.1") {
		t.Fatal("must be OpenAPI 3.1")
	}
	var documented []string
	for p, ops := range spec.Paths {
		for m := range ops {
			if m == "parameters" {
				continue
			}
			documented = append(documented, strings.ToUpper(m)+" "+p)
		}
	}
	a := New(nil, slog.New(slog.NewTextHandler(io.Discard, nil)), NewMetrics(), Options{Auth: HeaderAuth{}})
	var routed []string
	err := chi.Walk(a.Router().(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		route = strings.TrimSuffix(route, "/")
		if strings.HasPrefix(route, "/api/") || slices.Contains([]string{"/healthz", "/readyz", "/metrics"}, route) {
			if route == "/metrics" && method != "GET" {
				return nil
			}
			routed = append(routed, method+" "+route)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(documented)
	slices.Sort(routed)
	if !slices.Equal(documented, routed) {
		t.Fatalf("spec/router mismatch\nspec:   %v\nrouter: %v", documented, routed)
	}
}

func TestMiddlewareRecoverAndAuth(t *testing.T) {
	a := New(nil, slog.New(slog.NewTextHandler(io.Discard, nil)), NewMetrics(), Options{AccessLog: true,
		Auth: HeaderAuth{Resolve: func(_ context.Context, raw string) (domain.ID, error) {
			if raw == "1" {
				return 1, nil
			}
			return 0, domain.ErrUnauthenticated
		}}})
	h := a.observe(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))
	if rec.Code != 500 || rec.Header().Get("X-Request-Id") == "" {
		t.Fatalf("panic not recovered: %d", rec.Code)
	}
	var seen domain.ID
	protected := a.observe(a.requireUser(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen, _ = UserFrom(r.Context()) })))
	for hdr, code := range map[string]int{"": 401, "2": 401, "1": 200} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/", nil)
		if hdr != "" {
			req.Header.Set("X-User-Id", hdr)
		}
		req.Header.Set("X-Request-Id", "rid-1")
		protected.ServeHTTP(rec, req)
		if rec.Code != code || rec.Header().Get("X-Request-Id") != "rid-1" {
			t.Fatalf("%q -> %d", hdr, rec.Code)
		}
	}
	if seen != 1 {
		t.Fatal("user not propagated")
	}
}
