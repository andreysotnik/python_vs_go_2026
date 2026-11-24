package repository

import (
	"context"
	"strconv"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"

	"github.com/example/worldlib/internal/cursor"
	"github.com/example/worldlib/internal/domain"
)

type colKind int

const (
	kindText colKind = iota
	kindInt
	kindTime
	kindDate
)

// sortCol describes a whitelisted sort column. Every one has a (col, id) partial index.
type sortCol struct {
	col      string
	kind     colKind
	nullable bool
}

var bookSortCols = map[string]sortCol{
	"title":            {"b.title", kindText, false},
	"publication_year": {"b.publication_year", kindInt, true},
	"created_at":       {"b.created_at", kindTime, false},
}

var authorSortCols = map[string]sortCol{
	"full_name":  {"a.full_name", kindText, false},
	"birth_date": {"a.birth_date", kindDate, true},
	"created_at": {"a.created_at", kindTime, false},
}

// cursorArg converts the textual cursor value into a typed query argument.
func (c sortCol) cursorArg(v string) (any, error) {
	bad := domain.BadRequest("cursor", "malformed cursor value")
	switch c.kind {
	case kindInt:
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, bad
		}
		return n, nil
	case kindTime:
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			return nil, bad
		}
		return t, nil
	case kindDate:
		t, err := time.Parse(domain.DateLayout, v)
		if err != nil {
			return nil, bad
		}
		return t, nil
	default:
		return v, nil
	}
}

// KeysetPlan is the ordered list of queries ("phases") needed for one page.
// Non-nullable sorts need one phase; nullable sorts put NULLs last in both directions,
// which needs a second phase over "col IS NULL" (still served by the (col, id) index).
type KeysetPlan struct {
	Phases []sq.SelectBuilder
	Limit  int // page size; each phase fetches up to Limit+1 rows in total
}

// buildKeyset adds keyset predicates and ORDER BY to base.
func buildKeyset(base sq.SelectBuilder, sc sortCol, idCol string, desc bool, cur *cursor.Cursor, limit int) (KeysetPlan, error) {
	dir, cmp := "ASC", ">"
	if desc {
		dir, cmp = "DESC", "<"
	}
	order := []string{sc.col + " " + dir, idCol + " " + dir}
	plan := KeysetPlan{Limit: limit}
	rowCmp := func(b sq.SelectBuilder) (sq.SelectBuilder, error) {
		if cur == nil || cur.Value == nil {
			return b, nil
		}
		arg, err := sc.cursorArg(*cur.Value)
		if err != nil {
			return b, err
		}
		return b.Where("("+sc.col+", "+idCol+") "+cmp+" (?, ?)", arg, cur.ID), nil
	}
	if !sc.nullable {
		if cur != nil && cur.Value == nil {
			return plan, domain.BadRequest("cursor", "malformed cursor value")
		}
		b, err := rowCmp(base)
		if err != nil {
			return plan, err
		}
		plan.Phases = []sq.SelectBuilder{b.OrderBy(order...)}
		return plan, nil
	}
	nullPhase := base.Where(sc.col + " IS NULL").OrderBy(order...)
	if cur != nil && cur.Value == nil { // already inside the NULL tail
		plan.Phases = []sq.SelectBuilder{nullPhase.Where(idCol+" "+cmp+" ?", cur.ID)}
		return plan, nil
	}
	b, err := rowCmp(base.Where(sc.col + " IS NOT NULL"))
	if err != nil {
		return plan, err
	}
	plan.Phases = []sq.SelectBuilder{b.OrderBy(order...), nullPhase}
	return plan, nil
}

// SQL renders each phase with its LIMIT (for EXPLAIN tooling and tests).
func (p KeysetPlan) SQL() ([]string, [][]any, error) {
	var qs []string
	var as [][]any
	for _, ph := range p.Phases {
		q, a, err := ph.Limit(uint64(p.Limit + 1)).ToSql()
		if err != nil {
			return nil, nil, err
		}
		qs = append(qs, q)
		as = append(as, a)
	}
	return qs, as, nil
}

// runKeyset executes phases until Limit+1 rows are collected.
func runKeyset[T any](ctx context.Context, db DBTX, p KeysetPlan, scan func(pgx.Rows) (T, error)) ([]T, bool, error) {
	want := p.Limit + 1
	out := make([]T, 0, want)
	for _, ph := range p.Phases {
		if len(out) >= want {
			break
		}
		q, args, err := ph.Limit(uint64(want - len(out))).ToSql()
		if err != nil {
			return nil, false, err
		}
		rows, err := db.Query(ctx, q, args...)
		if err != nil {
			return nil, false, mapErr(err)
		}
		for rows.Next() {
			v, err := scan(rows)
			if err != nil {
				rows.Close()
				return nil, false, err
			}
			out = append(out, v)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, false, mapErr(err)
		}
	}
	more := len(out) > p.Limit
	if more {
		out = out[:p.Limit]
	}
	return out, more, nil
}

// escapeLike escapes LIKE wildcards in user input.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// estimateRows returns the planner's row estimate for "SELECT 1 FROM ... WHERE ..." (no COUNT(*)).
func estimateRows(ctx context.Context, db DBTX, b sq.SelectBuilder) (int64, error) {
	q, args, err := b.ToSql()
	if err != nil {
		return 0, err
	}
	var plan []struct {
		Plan struct {
			Rows float64 `json:"Plan Rows"`
		} `json:"Plan"`
	}
	if err := db.QueryRow(ctx, "EXPLAIN (FORMAT JSON) "+q, args...).Scan(&plan); err != nil {
		return 0, mapErr(err)
	}
	if len(plan) == 0 {
		return 0, nil
	}
	return int64(plan[0].Plan.Rows), nil
}

// FormatSortValue renders a sort value for a cursor.
func FormatSortValue(v any) *string {
	switch x := v.(type) {
	case nil:
		return nil
	case *int:
		if x == nil {
			return nil
		}
		s := strconv.Itoa(*x)
		return &s
	case *domain.Date:
		if x == nil {
			return nil
		}
		s := x.Format(domain.DateLayout)
		return &s
	case time.Time:
		s := x.UTC().Format(time.RFC3339Nano)
		return &s
	case string:
		return &x
	case float64:
		s := strconv.FormatFloat(x, 'g', -1, 64)
		return &s
	}
	return nil
}
