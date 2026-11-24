package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/example/worldlib/internal/domain"
)

// Problem is an RFC 7807 problem document.
type Problem struct {
	Type     string              `json:"type"`
	Title    string              `json:"title"`
	Status   int                 `json:"status"`
	Detail   string              `json:"detail,omitempty"`
	Instance string              `json:"instance,omitempty"`
	Errors   []domain.FieldError `json:"errors,omitempty"`
}

const problemBase = "https://library.example/problems/"

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeProblem(w http.ResponseWriter, r *http.Request, p Problem) {
	p.Instance = r.URL.Path
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}

// problemFor maps errors to problems.
func problemFor(err error) Problem {
	var ve *domain.ValidationError
	var pe *pgconn.PgError
	switch {
	case errors.As(err, &ve):
		if ve.Kind == domain.KindBadRequest {
			return Problem{Type: problemBase + "bad-request", Title: "Bad Request", Status: 400,
				Detail: "invalid request parameters", Errors: ve.Errors}
		}
		return Problem{Type: problemBase + "validation", Title: "Unprocessable Entity", Status: 422,
			Detail: "the entity failed validation", Errors: ve.Errors}
	case errors.Is(err, domain.ErrNotFound):
		return Problem{Type: problemBase + "not-found", Title: "Not Found", Status: 404, Detail: "resource not found"}
	case errors.Is(err, domain.ErrVersionConflict):
		return Problem{Type: problemBase + "version-conflict", Title: "Conflict", Status: 409,
			Detail: "the resource was modified concurrently; reload it and retry with the current version"}
	case errors.Is(err, domain.ErrDuplicate):
		return Problem{Type: problemBase + "duplicate", Title: "Conflict", Status: 409, Detail: err.Error()}
	case errors.Is(err, domain.ErrPreconditionRequired):
		return Problem{Type: problemBase + "precondition-required", Title: "Precondition Required", Status: 428,
			Detail: "send If-Match: \"<version>\" header or \"version\" in the body"}
	case errors.Is(err, domain.ErrUnauthenticated):
		return Problem{Type: problemBase + "unauthenticated", Title: "Unauthorized", Status: 401, Detail: err.Error()}
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &pe) && pe.Code == "57014":
		return Problem{Type: problemBase + "timeout", Title: "Gateway Timeout", Status: 504, Detail: "query took too long"}
	}
	return Problem{Type: problemBase + "internal", Title: "Internal Server Error", Status: 500}
}

func (a *API) fail(w http.ResponseWriter, r *http.Request, err error) {
	p := problemFor(err)
	if p.Status >= 500 {
		a.log.ErrorContext(r.Context(), "request failed", slog.String("error", err.Error()), slog.String("path", r.URL.Path))
	}
	writeProblem(w, r, p)
}
