package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/example/worldlib/internal/domain"
)

const maxBody = 1 << 20

func pathID(r *http.Request) (domain.ID, error) {
	id, ok := domain.ParseID(chi.URLParam(r, "id"))
	if !ok {
		return 0, domain.ErrNotFound
	}
	return id, nil
}

// decode reads a JSON body strictly (unknown fields rejected → 400).
func decode(r *http.Request, dst any) error {
	if ct := r.Header.Get("Content-Type"); ct != "" {
		mt, _, err := mime.ParseMediaType(ct)
		if err != nil || (mt != "application/json" && mt != "application/merge-patch+json") {
			return domain.BadRequest("Content-Type", "must be application/json")
		}
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var te *json.UnmarshalTypeError
		switch {
		case errors.As(err, &te):
			return domain.BadRequest(te.Field, "must be of type "+te.Type.String())
		case errors.Is(err, io.EOF):
			return domain.BadRequest("body", "request body is required")
		default:
			return domain.BadRequest("body", "malformed JSON: "+err.Error())
		}
	}
	if dec.More() {
		return domain.BadRequest("body", "trailing data after JSON object")
	}
	return nil
}

// ifMatch parses If-Match: "3", W/"3" or 3.
func ifMatch(r *http.Request) (*int, error) {
	h := strings.TrimSpace(r.Header.Get("If-Match"))
	if h == "" {
		return nil, nil
	}
	h = strings.Trim(strings.TrimPrefix(h, "W/"), `"`)
	v, err := strconv.Atoi(h)
	if err != nil || v < 1 {
		return nil, domain.BadRequest("If-Match", `must be the entity version, e.g. "3"`)
	}
	return &v, nil
}

func setETag(w http.ResponseWriter, version int) {
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, version))
}

func (a *API) user(r *http.Request) domain.ID {
	id, _ := UserFrom(r.Context())
	return id
}

// ---- users ----

func (a *API) createUser(w http.ResponseWriter, r *http.Request) {
	var in domain.UserInput
	if err := decode(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	u, err := a.svc.CreateUser(r.Context(), in)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/v1/users/"+strconv.FormatInt(int64(u.ID), 10))
	writeJSON(w, http.StatusCreated, u)
}

func (a *API) getUser(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err == nil {
		var u domain.User
		if u, err = a.svc.GetUser(r.Context(), id); err == nil {
			writeJSON(w, 200, u)
			return
		}
	}
	a.fail(w, r, err)
}

// ---- books ----

func (a *API) listBooks(w http.ResponseWriter, r *http.Request) {
	f, lp, err := domain.ParseBookQuery(r.URL.Query())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	p, err := a.svc.ListBooks(r.Context(), f, lp)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, 200, p)
}

func (a *API) getBook(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	b, err := a.svc.GetBook(r.Context(), id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	setETag(w, b.Version)
	writeJSON(w, 200, b)
}

func (a *API) createBook(w http.ResponseWriter, r *http.Request) {
	var in domain.BookInput
	if err := decode(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	b, err := a.svc.CreateBook(r.Context(), a.user(r), in)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/v1/books/"+strconv.FormatInt(int64(b.ID), 10))
	setETag(w, b.Version)
	writeJSON(w, http.StatusCreated, b)
}

func (a *API) updateBook(w http.ResponseWriter, r *http.Request, mode domain.Mode) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	im, err := ifMatch(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	var in domain.BookInput
	if err := decode(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	b, err := a.svc.UpdateBook(r.Context(), a.user(r), id, in, mode, im)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	setETag(w, b.Version)
	writeJSON(w, 200, b)
}

func (a *API) replaceBook(w http.ResponseWriter, r *http.Request) {
	a.updateBook(w, r, domain.ModeReplace)
}
func (a *API) patchBook(w http.ResponseWriter, r *http.Request) { a.updateBook(w, r, domain.ModePatch) }

func (a *API) deleteBook(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	im, err := ifMatch(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.svc.DeleteBook(r.Context(), a.user(r), id, im); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) history(w http.ResponseWriter, r *http.Request, entity string) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	lp, err := domain.ParsePageQuery(r.URL.Query(), "-changed_at")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	p, err := a.svc.History(r.Context(), entity, id, lp)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, 200, p)
}

func (a *API) bookHistory(w http.ResponseWriter, r *http.Request)   { a.history(w, r, "book") }
func (a *API) authorHistory(w http.ResponseWriter, r *http.Request) { a.history(w, r, "author") }

// ---- authors ----

func (a *API) listAuthors(w http.ResponseWriter, r *http.Request) {
	f, lp, err := domain.ParseAuthorQuery(r.URL.Query())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	p, err := a.svc.ListAuthors(r.Context(), f, lp)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, 200, p)
}

func (a *API) getAuthor(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	au, err := a.svc.GetAuthor(r.Context(), id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	setETag(w, au.Version)
	writeJSON(w, 200, au)
}

func (a *API) authorBooks(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	lp, err := domain.ParsePageQuery(r.URL.Query(), "book_id")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	p, err := a.svc.AuthorBooks(r.Context(), id, lp)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, 200, p)
}

func (a *API) createAuthor(w http.ResponseWriter, r *http.Request) {
	var in domain.AuthorInput
	if err := decode(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	au, err := a.svc.CreateAuthor(r.Context(), a.user(r), in)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/v1/authors/"+strconv.FormatInt(int64(au.ID), 10))
	setETag(w, au.Version)
	writeJSON(w, http.StatusCreated, au)
}

func (a *API) updateAuthor(w http.ResponseWriter, r *http.Request, mode domain.Mode) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	im, err := ifMatch(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	var in domain.AuthorInput
	if err := decode(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	au, err := a.svc.UpdateAuthor(r.Context(), a.user(r), id, in, mode, im)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	setETag(w, au.Version)
	writeJSON(w, 200, au)
}

func (a *API) replaceAuthor(w http.ResponseWriter, r *http.Request) {
	a.updateAuthor(w, r, domain.ModeReplace)
}
func (a *API) patchAuthor(w http.ResponseWriter, r *http.Request) {
	a.updateAuthor(w, r, domain.ModePatch)
}

func (a *API) deleteAuthor(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	im, err := ifMatch(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.svc.DeleteAuthor(r.Context(), a.user(r), id, im); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- search ----

func (a *API) search(w http.ResponseWriter, r *http.Request) {
	sp, err := domain.ParseSearchQuery(r.URL.Query())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	p, err := a.svc.Search(r.Context(), sp)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, 200, p)
}
