package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/example/worldlib/internal/domain"
)

type ctxKey int

const (
	ctxUser ctxKey = iota
	ctxRequestID
)

// Authenticator resolves the acting user. The header-based implementation can be swapped
// for a JWT validator without touching handlers.
type Authenticator interface {
	Authenticate(ctx context.Context, r *http.Request) (domain.ID, error)
}

// HeaderAuth trusts X-User-Id (simplified authentication) and checks the user exists.
type HeaderAuth struct {
	Resolve func(ctx context.Context, raw string) (domain.ID, error)
}

// Authenticate implements Authenticator.
func (h HeaderAuth) Authenticate(ctx context.Context, r *http.Request) (domain.ID, error) {
	raw := r.Header.Get("X-User-Id")
	if raw == "" {
		return 0, domain.ErrUnauthenticated
	}
	return h.Resolve(ctx, raw)
}

// UserFrom returns the authenticated user id.
func UserFrom(ctx context.Context) (domain.ID, bool) {
	id, ok := ctx.Value(ctxUser).(domain.ID)
	return id, ok
}

// requireUser authenticates mutating requests.
func (a *API) requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := a.auth.Authenticate(r.Context(), r)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxUser, id)))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

func newRequestID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Metrics holds Prometheus collectors on a private registry (no global state).
type Metrics struct {
	Registry *prometheus.Registry
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

// NewMetrics builds the collectors.
func NewMetrics() *Metrics {
	m := &Metrics{
		Registry: prometheus.NewRegistry(),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total", Help: "HTTP requests by route and status.",
		}, []string{"method", "route", "status"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "http_request_duration_seconds", Help: "HTTP request latency.",
			Buckets: []float64{.001, .0025, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		}, []string{"method", "route"}),
	}
	m.Registry.MustRegister(m.requests, m.duration)
	return m
}

// observe logs, measures and recovers each request.
func (a *API) observe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rid := r.Header.Get("X-Request-Id")
		if rid == "" || len(rid) > 64 {
			rid = newRequestID()
		}
		w.Header().Set("X-Request-Id", rid)
		ctx, cancel := context.WithTimeout(context.WithValue(r.Context(), ctxRequestID, rid), a.requestTimeout)
		defer cancel()
		rec := &statusRecorder{ResponseWriter: w}
		defer func() {
			if p := recover(); p != nil {
				a.log.ErrorContext(ctx, "panic", slog.Any("panic", p), slog.String("request_id", rid))
				if rec.status == 0 {
					writeProblem(rec, r, Problem{Type: problemBase + "internal", Title: "Internal Server Error", Status: 500})
				}
			}
			route := "unmatched"
			if rc := chi.RouteContext(ctx); rc != nil && rc.RoutePattern() != "" {
				route = rc.RoutePattern()
			}
			if rec.status == 0 {
				rec.status = http.StatusOK
			}
			el := time.Since(start)
			a.metrics.requests.WithLabelValues(r.Method, route, strconv.Itoa(rec.status)).Inc()
			a.metrics.duration.WithLabelValues(r.Method, route).Observe(el.Seconds())
			attrs := []any{
				slog.String("request_id", rid), slog.String("method", r.Method), slog.String("path", r.URL.Path),
				slog.String("route", route), slog.Int("status", rec.status), slog.Int("bytes", rec.bytes),
				slog.Float64("duration_ms", float64(el.Microseconds())/1000),
			}
			if uid, ok := UserFrom(r.Context()); ok {
				attrs = append(attrs, slog.Int64("user_id", int64(uid)))
			}
			if a.accessLog {
				a.log.InfoContext(ctx, "http_request", attrs...)
			}
		}()
		next.ServeHTTP(rec, r.WithContext(ctx))
	})
}
