// Package httpapi is the REST transport (chi): routing, decoding, error mapping, observability.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/example/worldlib/api"
	"github.com/example/worldlib/internal/service"
)

// API wires handlers to the service.
type API struct {
	svc            *service.Service
	auth           Authenticator
	log            *slog.Logger
	metrics        *Metrics
	requestTimeout time.Duration
	accessLog      bool
}

// Options configures the API.
type Options struct {
	RequestTimeout time.Duration
	AccessLog      bool
	Auth           Authenticator // defaults to HeaderAuth
}

// New builds the API.
func New(svc *service.Service, log *slog.Logger, m *Metrics, o Options) *API {
	a := &API{svc: svc, log: log, metrics: m, requestTimeout: o.RequestTimeout, accessLog: o.AccessLog, auth: o.Auth}
	if a.requestTimeout == 0 {
		a.requestTimeout = 10 * time.Second
	}
	if a.auth == nil {
		a.auth = HeaderAuth{Resolve: svc.Authenticate}
	}
	return a
}

// Router returns the HTTP handler.
func (a *API) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(a.observe)
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, r, Problem{Type: problemBase + "not-found", Title: "Not Found", Status: 404, Detail: "no such route"})
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, r, Problem{Type: problemBase + "method-not-allowed", Title: "Method Not Allowed", Status: 405})
	})

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	r.Get("/readyz", a.ready)
	r.Handle("/metrics", promhttp.HandlerFor(a.metrics.Registry, promhttp.HandlerOpts{}))
	r.Get("/openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write(api.OpenAPI)
	})
	r.Get("/docs", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(swaggerHTML))
	})

	r.Route("/api/v1", func(r chi.Router) {
		r.Route("/users", func(r chi.Router) {
			r.Post("/", a.createUser)
			r.Get("/{id}", a.getUser)
		})
		r.Route("/books", func(r chi.Router) {
			r.Get("/", a.listBooks)
			r.With(a.requireUser).Post("/", a.createBook)
			r.Route("/{id}", func(r chi.Router) {
				r.Get("/", a.getBook)
				r.Get("/history", a.bookHistory)
				r.With(a.requireUser).Put("/", a.replaceBook)
				r.With(a.requireUser).Patch("/", a.patchBook)
				r.With(a.requireUser).Delete("/", a.deleteBook)
			})
		})
		r.Route("/authors", func(r chi.Router) {
			r.Get("/", a.listAuthors)
			r.With(a.requireUser).Post("/", a.createAuthor)
			r.Route("/{id}", func(r chi.Router) {
				r.Get("/", a.getAuthor)
				r.Get("/books", a.authorBooks)
				r.Get("/history", a.authorHistory)
				r.With(a.requireUser).Put("/", a.replaceAuthor)
				r.With(a.requireUser).Patch("/", a.patchAuthor)
				r.With(a.requireUser).Delete("/", a.deleteAuthor)
			})
		})
		r.Get("/search", a.search)
	})
	return r
}

func (a *API) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := a.svc.Ready(ctx); err != nil {
		writeProblem(w, r, Problem{Type: problemBase + "not-ready", Title: "Service Unavailable", Status: 503, Detail: "database unavailable"})
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ready"})
}

const swaggerHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>World Library API</title>
<link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5.17.14/swagger-ui.css"></head>
<body><div id="swagger-ui"></div>
<script src="https://unpkg.com/swagger-ui-dist@5.17.14/swagger-ui-bundle.js"></script>
<script>window.ui = SwaggerUIBundle({url: '/openapi.yaml', dom_id: '#swagger-ui', deepLinking: true});</script>
</body></html>`
