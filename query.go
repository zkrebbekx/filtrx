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
	from       *sourceSpec
	columns    []string
	cond       Cond
	groupBy    []string
	having     Cond
	order      []orderTerm
	paging     PagingParams
	seek       *seekState
	dialect    Dialect
	unfiltered bool
	err        error
}

type orderTerm struct {
	col   string
	desc  bool
	nulls nullsPos
}

// nullsPos is where NULLs sort within an order term. The default leaves the
// dialect's own placement, which is only safe for NOT NULL columns; keyset
// paging over a nullable column needs an explicit position.
type nullsPos int

const (
	nullsDefault nullsPos = iota
	nullsFirst
	nullsLast
)

func flipNulls(p nullsPos) nullsPos {
	switch p {
	case nullsFirst:
		return nullsLast
	case nullsLast:
		return nullsFirst
	default:
		return nullsDefault
	}
}

// From starts a query against the given table or view.
func From(table string) *Query {
	return &Query{from: &sourceSpec{table: table}, dialect: Postgres}
}

// For starts a query whose table and joins are declared by a filter struct's
// Table and Join marker fields, and applies that struct as the WHERE filter. It
// is the entry point for join-backed filters:
//
//	type OrderFilter struct {
//		Base   filtrx.Table `table:"users" as:"u"`
//		Orders filtrx.Join  `table:"orders" as:"o" on:"o.user_id = u.id" type:"left"`
//		Status filtrx.Text  `col:"u.status"`
//	}
//	q := filtrx.For(OrderFilter{Status: filtrx.Text{Eq: filtrx.Some("active")}})
//
// The filter must declare a Table field; otherwise the deferred error surfaces
// at List or Count.
func For(filter any) *Query {
	q := &Query{dialect: Postgres}
	q.Where(filter)
	if q.err == nil && (q.from == nil || q.from.table == "") {
		q.err = fmt.Errorf("%w: For requires a filtrx.Table marker field", ErrCompile)
	}
	return q
}

// Select sets the columns to return. With none given the query selects all
// columns ("*"), which is the default.
func (q *Query) Select(cols ...string) *Query {
	q.columns = cols
	return q
}

// Where compiles a tagged filter struct (see the package-level Where) and uses
// it as the query's condition. If the struct declares From/Join marker fields,
// they also set the query's table and joins. A compile error is deferred and
// surfaced by List.
func (q *Query) Where(filter any) *Query {
	c, src, err := compileFilter(filter)
	if err != nil {
		q.err = err
		return q
	}
	q.cond = c
	if src != nil {
		// The struct's declared source (table + joins) takes over the query's
		// FROM. src is the cached, read-only spec; never mutate it here.
		q.from = src
	}
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

// OrderByNulls appends a sort on col with an explicit NULL placement. Use it
// when col is nullable and the query pages by keyset (Seek): keyset's seek
// predicate must know whether NULLs sort first or last, which the database's
// default placement (and its variation across dialects) does not pin down.
// first true places NULLs before non-NULLs, false places them after.
func (q *Query) OrderByNulls(col string, desc, first bool) *Query {
	pos := nullsLast
	if first {
		pos = nullsFirst
	}
	q.order = append(q.order, orderTerm{col: col, desc: desc, nulls: pos})
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

// GroupBy adds a GROUP BY clause over the given columns. Pair it with aggregate
// projection columns via Select and filter the groups with Having. The COUNT(*)
// OVER() fast total then counts result groups, so pagination over grouped rows
// still reports an accurate total.
func (q *Query) GroupBy(cols ...string) *Query {
	q.groupBy = append(q.groupBy, cols...)
	return q
}

// Having sets a HAVING condition, applied after grouping. Its placeholders
// follow the WHERE arguments. A predicate on a plain grouped column can use the
// ordinary constructors (filtrx.Gt("price", 100)); for an aggregate, whose
// expression must not be quoted as an identifier, use filtrx.Raw —
// filtrx.Raw("COUNT(*) > ?", 1).
func (q *Query) Having(c Cond) *Query {
	q.having = c
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

	if q.seek != nil {
		return listKeyset(ctx, db, q, dest)
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

// fromClause renders the FROM target: the table (with optional alias) plus any
// joins. Joins come only from a filter struct's marker fields.
func (q *Query) fromClause() string {
	if q.from == nil {
		return ""
	}
	return q.from.render(q.dialect)
}

// count runs SELECT COUNT(*) for the filter, ignoring ordering and paging. When
// the query groups, the count is the number of groups, obtained by wrapping the
// grouped SELECT in a COUNT(*) subquery.
func count(ctx context.Context, db sqlx.QueryerContext, q *Query, where string, args []any) (int, error) {
	var sb strings.Builder
	if q.hasGrouping() {
		sb.WriteString("SELECT COUNT(*) FROM (SELECT 1 FROM ")
		sb.WriteString(q.fromClause())
		if where != "" {
			sb.WriteString(" WHERE ")
			sb.WriteString(where)
		}
		args = q.writeGroupHaving(&sb, args)
		sb.WriteString(") AS _filtrx_grp")
	} else {
		sb.WriteString("SELECT COUNT(*) FROM ")
		sb.WriteString(q.fromClause())
		if where != "" {
			sb.WriteString(" WHERE ")
			sb.WriteString(where)
		}
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

	var (
		scanned int
		sink    int // receives the window total (identical on every row)
	)
	for rows.Next() {
		// Allocate a fresh row per iteration. Destinations must not be reused
		// across rows: a field reached through a pointer (or a buffer-aliasing
		// Scanner like sql.RawBytes) would otherwise be shared, so every appended
		// row would end up holding the last row's value.
		var (
			row    T
			ignore any
		)
		rv := reflect.ValueOf(&row).Elem()
		holders := make([]any, len(cols))
		for i, tr := range traversals {
			switch {
			case cols[i] == totalColumn:
				holders[i] = &sink
			case len(tr) == 0:
				// Column has no destination field. Discard it, matching an Unsafe
				// sqlx session — SELECT * against a table with columns the scan
				// struct omits is common and must not fail.
				holders[i] = &ignore
			default:
				holders[i] = reflectx.FieldByIndexes(rv, tr).Addr().Interface()
			}
		}
		if err := rows.Scan(holders...); err != nil {
			return 0, 0, fmt.Errorf("%w: scan: %w", ErrQuery, err)
		}
		*dest = append(*dest, row)
		scanned++
	}
	if err := rows.Err(); err != nil {
		return 0, 0, fmt.Errorf("%w: %w", ErrQuery, err)
	}
	return scanned, sink, nil
}

// writeProjection writes the SELECT column list: the explicit columns, or the
// base table's columns when a join alias is set, or "*".
func (q *Query) writeProjection(sb *strings.Builder) {
	switch {
	case len(q.columns) > 0:
		for i, c := range q.columns {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(c)
		}
	case q.from != nil && q.from.alias != "":
		// With an aliased source (joins), a bare * pulls every joined table's
		// columns (and collides on shared names like id). Default to the base
		// table's columns only.
		sb.WriteString(q.dialect.quoteIdent(q.from.alias))
		sb.WriteString(".*")
	default:
		sb.WriteString("*")
	}
}

// writeGroupHaving appends GROUP BY and HAVING after the WHERE clause, returning
// the running argument list (whereArgs plus any HAVING args). HAVING placeholders
// continue the WHERE numbering. HAVING is valid without GROUP BY (an aggregate
// over the whole table), so the two are emitted independently.
func (q *Query) writeGroupHaving(sb *strings.Builder, whereArgs []any) []any {
	if len(q.groupBy) > 0 {
		sb.WriteString(" GROUP BY ")
		for i, c := range q.groupBy {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(q.dialect.quoteIdent(c))
		}
	}
	if q.having == nil {
		return whereArgs
	}
	hs, ha := buildAt(q.having, q.dialect, len(whereArgs))
	if hs == "" {
		return whereArgs
	}
	sb.WriteString(" HAVING ")
	sb.WriteString(hs)
	return append(whereArgs, ha...)
}

// hasGrouping reports whether the query groups or filters groups, so counting
// must wrap the grouped result rather than COUNT(*) the base rows.
func (q *Query) hasGrouping() bool {
	return len(q.groupBy) > 0 || q.having != nil
}

// writeOrder appends an ORDER BY clause for the given terms, quoting each column
// per the dialect and applying any explicit NULL placement. It writes nothing
// for an empty term list.
func (q *Query) writeOrder(sb *strings.Builder, order []orderTerm) {
	if len(order) == 0 {
		return
	}
	sb.WriteString(" ORDER BY ")
	for i, o := range order {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(orderClause(q.dialect, q.dialect.quoteIdent(o.col), o.desc, o.nulls))
	}
}

func (q *Query) buildSelect(where string, whereArgs []any, limit, offset int, window bool) (string, []any) {
	var sb strings.Builder
	sb.WriteString("SELECT ")
	q.writeProjection(&sb)
	if window {
		sb.WriteString(", COUNT(*) OVER() AS ")
		sb.WriteString(totalColumn)
	}
	sb.WriteString(" FROM ")
	sb.WriteString(q.fromClause())
	if where != "" {
		sb.WriteString(" WHERE ")
		sb.WriteString(where)
	}
	args := q.writeGroupHaving(&sb, whereArgs)
	q.writeOrder(&sb, q.order)
	n := len(args)
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
