package filtrx

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/jmoiron/sqlx/reflectx"
)

// totalColumn is the alias under which the window-function total is selected. It
// is unmapped by user structs and recovered separately during the scan.
const totalColumn = "_filtrx_total"

// defaultMapper mirrors sqlx's default ("db" struct tags) so List scans the same
// structs sqlx.Select would.
var defaultMapper = reflectx.NewMapper("db")

// Query is a fluent description of a SELECT to be listed: its source, columns,
// filter, ordering and paging. Build one with From and the chaining methods,
// then pass it to List. The zero dialect is Postgres; set another with On.
type Query struct {
	table   string
	columns []string
	cond    Cond
	order   []orderTerm
	paging  PagingParams
	dialect Dialect
	err     error
}

type orderTerm struct {
	col  string
	desc bool
}

// From starts a query against the given table or view.
func From(table string) *Query {
	return &Query{table: table, dialect: Postgres}
}

// Select sets the columns to return. With none given the query selects all
// columns ("*"), which is the default.
func (q *Query) Select(cols ...string) *Query {
	q.columns = cols
	return q
}

// Where compiles a tagged filter struct (see the package-level Where) and uses
// it as the query's condition. A compile error is deferred and surfaced by List.
func (q *Query) Where(filter any) *Query {
	c, err := Where(filter)
	if err != nil {
		q.err = err
		return q
	}
	q.cond = c
	return q
}

// Cond sets the query's condition from an already-built tree, bypassing struct
// compilation. Useful with the And/Or/Eq constructors.
func (q *Query) Cond(c Cond) *Query {
	q.cond = c
	return q
}

// OrderBy appends an ascending sort on col. Stable pagination needs a total
// order, so end with a unique column (commonly the primary key).
func (q *Query) OrderBy(col string) *Query {
	q.order = append(q.order, orderTerm{col: col})
	return q
}

// OrderByDesc appends a descending sort on col.
func (q *Query) OrderByDesc(col string) *Query {
	q.order = append(q.order, orderTerm{col: col, desc: true})
	return q
}

// Sort appends ordering parsed from a request, safely. spec is a comma-separated
// list of sort keys such as "name,-created"; a leading "-" means descending. Only
// keys present in allowed are accepted, and the mapped value is the real column —
// so a sort parameter from a request can never inject a column name. An unknown
// key defers an error that List surfaces.
//
//	q.Sort(r.URL.Query().Get("sort"), map[string]string{
//		"name":    "name",
//		"created": "created_at",
//	})
func (q *Query) Sort(spec string, allowed map[string]string) *Query {
	if spec == "" {
		return q
	}
	for _, key := range strings.Split(spec, ",") {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		desc := false
		switch key[0] {
		case '-':
			desc, key = true, key[1:]
		case '+':
			key = key[1:]
		}
		col, ok := allowed[key]
		if !ok {
			q.err = fmt.Errorf("%w: sort key %q is not allowed", ErrCompile, key)
			return q
		}
		q.order = append(q.order, orderTerm{col: col, desc: desc})
	}
	return q
}

// Page sets the pagination arguments.
func (q *Query) Page(p PagingParams) *Query {
	q.paging = p
	return q
}

// On selects the SQL dialect (Postgres, MySQL, SQLite). Postgres is the default.
func (q *Query) On(d Dialect) *Query {
	q.dialect = d
	return q
}

// List runs the query and pagination as one operation, scanning rows into dest
// and returning a PageInfo. When the request asks for a total (IncludeTotal, or
// any Last request) it is obtained from COUNT(*) OVER() in the same statement —
// the fast total — falling back to a COUNT only when the page itself is empty.
//
// T must be a struct mappable by sqlx ("db" tags). db is any sqlx querier:
// *sqlx.DB, *sqlx.Tx, or *sqlx.Conn.
func List[T any](ctx context.Context, db sqlx.QueryerContext, q *Query, dest *[]T) (PageInfo, error) {
	if q.err != nil {
		return PageInfo{}, fmt.Errorf("%w: %w", ErrCompile, q.err)
	}

	where, whereArgs := Build(q.cond, q.dialect)
	paginator, needsTotal := Paginate(q.paging)

	var info PageInfo

	// No records to fetch: report an accurate total for the empty page if asked.
	if paginator == nil {
		if needsTotal {
			n, err := count(ctx, db, q, where, whereArgs)
			if err != nil {
				return PageInfo{}, err
			}
			info.Total = n
		}
		return info, nil
	}

	// Last-style paging measures its offset from the end, so the count must be
	// known before the window can be computed.
	var preCounted bool
	if needsTotal {
		n, err := count(ctx, db, q, where, whereArgs)
		if err != nil {
			return PageInfo{}, err
		}
		info.Total, preCounted = n, true
	}

	limit, offset := paginator(info.Total)
	info.Offset = offset

	wantWindow := q.paging.IncludeTotal && !preCounted
	rowsScanned, windowTotal, err := selectRows(ctx, db, q, where, whereArgs, limit, offset, wantWindow, dest)
	if err != nil {
		return PageInfo{}, err
	}

	// A bounded query fetches limit+1 rows; the extra one reveals that more
	// records lie past the window. An unbounded query (limit <= 0) returns
	// everything, so it is never truncated.
	if limit > 0 && rowsScanned > limit {
		info.Truncated = true
		*dest = (*dest)[:limit]
	}

	switch {
	case preCounted:
		// Total already set.
	case q.paging.IncludeTotal && rowsScanned > 0:
		info.Total = windowTotal
	case q.paging.IncludeTotal: // empty page: the window produced no row
		n, cerr := count(ctx, db, q, where, whereArgs)
		if cerr != nil {
			return PageInfo{}, cerr
		}
		info.Total = n
	}
	return info, nil
}

// Count returns the number of rows matching the query's filter, ignoring its
// ordering and pagination. It is the standalone COUNT for callers that want a
// total without fetching any rows.
func (q *Query) Count(ctx context.Context, db sqlx.QueryerContext) (int, error) {
	if q.err != nil {
		return 0, fmt.Errorf("%w: %w", ErrCompile, q.err)
	}
	where, args := Build(q.cond, q.dialect)
	return count(ctx, db, q, where, args)
}

// count runs SELECT COUNT(*) for the filter, ignoring ordering and paging.
func count(ctx context.Context, db sqlx.QueryerContext, q *Query, where string, args []any) (int, error) {
	var sb strings.Builder
	sb.WriteString("SELECT COUNT(*) FROM ")
	sb.WriteString(q.dialect.quoteIdent(q.table))
	if where != "" {
		sb.WriteString(" WHERE ")
		sb.WriteString(where)
	}
	var n int
	row := db.QueryRowxContext(ctx, sb.String(), args...)
	if err := row.Scan(&n); err != nil {
		return 0, fmt.Errorf("%w: count: %w", ErrQuery, err)
	}
	return n, nil
}

// selectRows builds and runs the windowed (or plain) SELECT, scanning each row
// into a T while recovering the window total column when present. It returns the
// number of rows scanned (before any truncation) and the total.
func selectRows[T any](ctx context.Context, db sqlx.QueryerContext, q *Query, where string, whereArgs []any, limit, offset int, window bool, dest *[]T) (int, int, error) {
	query, args := q.buildSelect(where, whereArgs, limit, offset, window)

	rows, err := db.QueryxContext(ctx, query, args...)
	if err != nil {
		return 0, 0, fmt.Errorf("%w: %w", ErrQuery, err)
	}
	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	if err != nil {
		return 0, 0, fmt.Errorf("%w: columns: %w", ErrQuery, err)
	}

	var zero T
	traversals := defaultMapper.TraversalsByName(reflect.TypeOf(zero), cols)

	// Build the scan destinations once and reuse them for every row. The holders
	// point into a single row buffer t whose address is stable; each scanned row
	// is copied by value into dest, so the buffer can be overwritten safely.
	var (
		row    T
		sink   int // receives the window total (identical on every row)
		ignore any // receives any column with no destination field
	)
	rv := reflect.ValueOf(&row).Elem()
	holders := make([]any, len(cols))
	for i, tr := range traversals {
		switch {
		case cols[i] == totalColumn:
			holders[i] = &sink
		case len(tr) == 0:
			// Column has no destination field. Discard it, matching the behaviour
			// of an Unsafe sqlx session — SELECT * against a table with columns
			// the scan struct omits is common and must not fail.
			holders[i] = &ignore
		default:
			holders[i] = reflectx.FieldByIndexes(rv, tr).Addr().Interface()
		}
	}

	var scanned int
	for rows.Next() {
		if err := rows.Scan(holders...); err != nil {
			return 0, 0, fmt.Errorf("%w: scan: %w", ErrQuery, err)
		}
		*dest = append(*dest, row)
		scanned++
	}
	if err := rows.Err(); err != nil {
		return 0, 0, fmt.Errorf("%w: %w", ErrQuery, err)
	}
	total := sink
	return scanned, total, nil
}

func (q *Query) buildSelect(where string, whereArgs []any, limit, offset int, window bool) (string, []any) {
	var sb strings.Builder
	sb.WriteString("SELECT ")
	if len(q.columns) == 0 {
		sb.WriteString("*")
	} else {
		for i, c := range q.columns {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(c)
		}
	}
	if window {
		sb.WriteString(", COUNT(*) OVER() AS ")
		sb.WriteString(totalColumn)
	}
	sb.WriteString(" FROM ")
	sb.WriteString(q.dialect.quoteIdent(q.table))
	if where != "" {
		sb.WriteString(" WHERE ")
		sb.WriteString(where)
	}
	if len(q.order) > 0 {
		sb.WriteString(" ORDER BY ")
		for i, o := range q.order {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(q.dialect.quoteIdent(o.col))
			if o.desc {
				sb.WriteString(" DESC")
			}
		}
	}
	args := whereArgs
	n := len(whereArgs)
	switch {
	case limit > 0:
		// Fetch one extra row to detect whether more records follow the window.
		sb.WriteString(" LIMIT ")
		sb.WriteString(q.dialect.placeholder(n + 1))
		sb.WriteString(" OFFSET ")
		sb.WriteString(q.dialect.placeholder(n + 2))
		args = append(args, limit+1, offset)
	case offset > 0:
		// Unbounded but skipping rows: some dialects need an explicit "all rows"
		// limit before OFFSET is legal.
		if al := q.dialect.allRowsLimit(); al != "" {
			sb.WriteString(" LIMIT ")
			sb.WriteString(al)
		}
		sb.WriteString(" OFFSET ")
		sb.WriteString(q.dialect.placeholder(n + 1))
		args = append(args, offset)
	}
	// limit <= 0 and offset == 0: no LIMIT/OFFSET clause — return everything.
	return sb.String(), args
}
