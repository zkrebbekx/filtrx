package filtrx

import (
	"bytes"
	"context"
	"database/sql/driver"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/jmoiron/sqlx/reflectx"
)

// Cursor is an opaque, URL-safe pagination token. It encodes the ordering-column
// values of a row so the next page can seek directly past it. Treat it as
// opaque: obtain one from a PageInfo and hand it back via SeekParams; do not
// construct or parse it.
type Cursor string

// SeekParams configures keyset (seek) pagination — the O(1)-per-page alternative
// to offset paging that does not slow down on deep pages. The query's OrderBy
// terms define both the sort and the key. Page forward by passing the previous
// page's EndCursor as After; page backward by passing a page's StartCursor as
// Before. Leave both empty for the first page.
//
// Keyset columns must be NOT NULL and form a total order — end the ordering with
// a unique column (commonly the primary key). Unlike offset paging, keyset does
// not compute a total by default; set IncludeTotal to pay one extra COUNT.
type SeekParams struct {
	After        Cursor // page forward, strictly after this cursor
	Before       Cursor // page backward, strictly before this cursor
	Size         int    // page size (required, must be positive)
	IncludeTotal bool   // also run a COUNT for the filter's total
}

// seekState is the resolved keyset request stored on a Query.
type seekState struct {
	cursor Cursor
	before bool
	size   int
	total  bool
}

// Seek switches the query to keyset pagination with the given parameters. It
// takes precedence over Page: once Seek is set, List uses the keyset path and
// ignores any PagingParams. The query must declare at least one OrderBy, whose
// columns are the cursor key.
func (q *Query) Seek(p SeekParams) *Query {
	if p.After != "" && p.Before != "" {
		q.err = fmt.Errorf("%w: Seek cannot set both After and Before", ErrCompile)
		return q
	}
	s := &seekState{size: p.Size, total: p.IncludeTotal}
	if p.Before != "" {
		s.cursor, s.before = p.Before, true
	} else {
		s.cursor = p.After
	}
	q.seek = s
	return q
}

// keysetResult is the outcome of a keyset scan shared by List (offset-style
// PageInfo) and ListConnection (Relay edges): one opaque cursor per returned
// row, whether a further page exists in the paging direction, and the total when
// it was requested.
type keysetResult struct {
	cursors   []Cursor
	truncated bool
	total     int
	totalSet  bool
}

// listKeyset runs a Query in keyset mode and reports the window as a PageInfo.
func listKeyset[T any](ctx context.Context, db sqlx.QueryerContext, q *Query, dest *[]T) (PageInfo, error) {
	res, err := runKeyset(ctx, db, q, dest)
	if err != nil {
		return PageInfo{}, err
	}
	info := PageInfo{Truncated: res.truncated}
	if n := len(res.cursors); n > 0 {
		info.StartCursor = res.cursors[0]
		info.EndCursor = res.cursors[n-1]
	}
	if res.totalSet {
		info.Total = res.total
	}
	return info, nil
}

// runKeyset seeks past the cursor (if any), fetches one extra row to detect a
// following page, restores natural order for a backward page, and returns one
// cursor per returned row.
func runKeyset[T any](ctx context.Context, db sqlx.QueryerContext, q *Query, dest *[]T) (keysetResult, error) {
	if len(q.order) == 0 {
		return keysetResult{}, fmt.Errorf("%w: keyset pagination requires at least one OrderBy", ErrCompile)
	}
	if q.seek.size <= 0 {
		return keysetResult{}, fmt.Errorf("%w: Seek size must be positive", ErrCompile)
	}
	if q.hasRelevanceOrder() {
		return keysetResult{}, fmt.Errorf("%w: keyset pagination cannot order by relevance", ErrCompile)
	}

	var zero T
	rowType := reflect.TypeOf(zero)

	// Each order column must map to a field of the scan struct so its value can be
	// read back into a cursor. Order columns may be qualified (u.id); the field is
	// matched on the unqualified name.
	keyCols := make([]string, len(q.order))
	for i, o := range q.order {
		keyCols[i] = unqualify(o.col)
	}
	keyTraversals := defaultMapper.TraversalsByName(rowType, keyCols)
	for i, tr := range keyTraversals {
		if len(tr) == 0 {
			return keysetResult{}, fmt.Errorf("%w: keyset order column %q has no matching field on %s", ErrCompile, q.order[i].col, rowType)
		}
	}

	// Seek predicate from the cursor, AND-ed onto the filter (with its soft-delete
	// scope). The first page has no cursor and selects from the start of the order.
	base := q.effectiveCond()
	cond := base
	if q.seek.cursor != "" {
		vals, err := decodeCursor(q.seek.cursor)
		if err != nil {
			return keysetResult{}, fmt.Errorf("%w: %w", ErrCompile, err)
		}
		if len(vals) != len(q.order) {
			return keysetResult{}, fmt.Errorf("%w: cursor carries %d values but the query orders by %d columns", ErrCompile, len(vals), len(q.order))
		}
		cond = And(base, keysetCond(q.order, vals, q.seek.before))
	}
	where, whereArgs := Build(cond, q.dialect)

	// A backward page is fetched in reversed order (so the rows nearest the cursor
	// come first under LIMIT) and flipped back to natural order before returning.
	order := q.order
	if q.seek.before {
		order = make([]orderTerm, len(q.order))
		for i, o := range q.order {
			order[i] = orderTerm{col: o.col, desc: !o.desc, nulls: flipNulls(o.nulls)}
		}
	}

	query, args := q.buildKeysetSelect(where, whereArgs, order, q.seek.size+1)
	rows, err := db.QueryxContext(ctx, query, args...)
	if err != nil {
		return keysetResult{}, fmt.Errorf("%w: %w", ErrQuery, err)
	}
	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	if err != nil {
		return keysetResult{}, fmt.Errorf("%w: columns: %w", ErrQuery, err)
	}
	colTraversals := defaultMapper.TraversalsByName(rowType, cols)

	start := len(*dest)
	var keys [][]any
	for rows.Next() {
		var (
			row    T
			ignore any
		)
		rv := reflect.ValueOf(&row).Elem()
		holders := make([]any, len(cols))
		for i, tr := range colTraversals {
			if len(tr) == 0 {
				holders[i] = &ignore
			} else {
				holders[i] = reflectx.FieldByIndexes(rv, tr).Addr().Interface()
			}
		}
		if err := rows.Scan(holders...); err != nil {
			return keysetResult{}, fmt.Errorf("%w: scan: %w", ErrQuery, err)
		}
		key := make([]any, len(keyTraversals))
		for i, tr := range keyTraversals {
			key[i] = reflectx.FieldByIndexes(rv, tr).Interface()
		}
		*dest = append(*dest, row)
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return keysetResult{}, fmt.Errorf("%w: %w", ErrQuery, err)
	}

	var res keysetResult
	// The extra row reveals a further page in the paging direction; drop it.
	if len(keys) > q.seek.size {
		res.truncated = true
		*dest = (*dest)[:start+q.seek.size]
		keys = keys[:q.seek.size]
	}
	// Restore natural order for a backward page.
	if q.seek.before {
		reverse((*dest)[start:])
		reverse(keys)
	}

	res.cursors = make([]Cursor, len(keys))
	for i, k := range keys {
		c, cerr := encodeCursor(k)
		if cerr != nil {
			return keysetResult{}, cerr
		}
		res.cursors[i] = c
	}

	if q.seek.total {
		baseWhere, baseArgs := Build(q.effectiveCond(), q.dialect)
		n, cerr := count(ctx, db, q, baseWhere, baseArgs)
		if cerr != nil {
			return keysetResult{}, cerr
		}
		res.total, res.totalSet = n, true
	}
	return res, nil
}

// buildKeysetSelect renders the seek query: projection, FROM, optional WHERE, the
// (possibly reversed) ORDER BY, and a plain LIMIT. There is no OFFSET — that is
// the point of keyset paging.
func (q *Query) buildKeysetSelect(where string, whereArgs []any, order []orderTerm, limit int) (string, []any) {
	var sb strings.Builder
	sb.WriteString("SELECT ")
	q.writeProjection(&sb)
	sb.WriteString(" FROM ")
	sb.WriteString(q.fromClause())
	if where != "" {
		sb.WriteString(" WHERE ")
		sb.WriteString(where)
	}
	args := q.writeGroupHaving(&sb, whereArgs)
	args = append(args, q.writeOrder(&sb, order, len(args))...)
	sb.WriteString(" LIMIT ")
	sb.WriteString(q.dialect.placeholder(len(args) + 1))
	return sb.String(), append(args, limit)
}

// keysetCond builds the lexicographic "row is past the boundary" predicate for
// the given order terms and boundary values. For a forward page (before=false)
// it selects rows strictly after the boundary in sort order; for a backward page
// it selects rows strictly before. It expands to an OR of AND-prefixes so it is
// portable across dialects and correct for mixed ASC/DESC ordering — unlike a
// row-value comparison, which cannot mix directions.
//
//	ORDER BY a ASC, b DESC, with values (va, vb), forward:
//	  (a > va) OR (a = va AND b < vb)
//
// A boundary value may be nil when the order term declares a NULL placement
// (OrderByNulls); the equality prefix becomes IS NULL and the comparison honours
// where NULLs sort. A position that has nothing sorting strictly after it (e.g. a
// NULL boundary under NULLS LAST going forward) contributes no branch.
func keysetCond(order []orderTerm, vals []any, before bool) Cond {
	var branches []Cond
	for i := range order {
		cmp := afterCompare(order[i], vals[i], before)
		if cmp == nil {
			continue
		}
		ands := make([]Cond, 0, i+1)
		for j := 0; j < i; j++ {
			ands = append(ands, nullEq(order[j].col, vals[j]))
		}
		ands = append(ands, cmp)
		branches = append(branches, And(ands...))
	}
	if len(branches) == 0 {
		// Nothing sorts strictly after the boundary (it is the last row in order):
		// the next page is empty. Match nothing rather than emitting an empty group.
		return Raw("1=0")
	}
	return Or(branches...)
}

// nullEq is a NULL-safe equality for an equality prefix: IS NULL when the
// boundary value is nil, plain equality otherwise.
func nullEq(col string, v any) Cond {
	if v == nil {
		return IsNull(col)
	}
	return Eq(col, v)
}

// afterCompare returns the predicate selecting rows that sort strictly after
// value v at column t, honouring direction and NULL placement. It returns nil
// when nothing sorts after v at this position. Paging backward (before) reverses
// both the direction and the NULL placement.
func afterCompare(t orderTerm, v any, before bool) Cond {
	ascending := t.desc == before
	nulls := t.nulls
	if before {
		nulls = flipNulls(nulls)
	}
	if v == nil {
		// After a NULL: under NULLS FIRST the non-NULLs all follow; under NULLS
		// LAST nothing follows (deeper tiebreakers are handled by other branches).
		if nulls == nullsFirst {
			return IsNotNull(t.col)
		}
		return nil
	}
	var base Cond
	if ascending {
		base = Gt(t.col, v)
	} else {
		base = Lt(t.col, v)
	}
	// Under NULLS LAST the NULLs sort after every non-NULL, so they too are "after"
	// a non-NULL boundary; include them.
	if nulls == nullsLast {
		return Or(base, IsNull(t.col))
	}
	return base
}

// unqualify strips a table qualifier from a column reference: "u.id" -> "id".
func unqualify(col string) string {
	if i := strings.LastIndex(col, "."); i >= 0 {
		return col[i+1:]
	}
	return col
}

// reverse flips a slice in place. It restores natural order after a backward
// keyset page is fetched in reversed order, for both the row and key slices.
func reverse[T any](s []T) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

// cursorElem is one ordering value with a type tag so the cursor round-trips
// across the wire without losing the distinction the database needs (an integer
// id must come back an integer, a timestamp a time).
type cursorElem struct {
	T string `json:"t"`
	V any    `json:"v"`
}

// encodeCursor packs boundary values into an opaque URL-safe token. A NULL key
// (a nil pointer, an unset Opt, or a NULL sql.Null* value) is encoded as a null
// element; it is only meaningful for an order term that declares a NULL position
// via OrderByNulls.
func encodeCursor(vals []any) (Cursor, error) {
	elems := make([]cursorElem, len(vals))
	for i, v := range vals {
		e, err := toCursorElem(v)
		if err != nil {
			return "", fmt.Errorf("%w: %w", ErrQuery, err)
		}
		elems[i] = e
	}
	raw, err := json.Marshal(elems)
	if err != nil {
		return "", fmt.Errorf("%w: cursor encode: %w", ErrQuery, err)
	}
	return Cursor(base64.RawURLEncoding.EncodeToString(raw)), nil
}

func toCursorElem(v any) (cursorElem, error) {
	switch x := v.(type) {
	case nil:
		return cursorElem{T: "0"}, nil
	case time.Time:
		return cursorElem{"t", x.Format(time.RFC3339Nano)}, nil
	case []byte:
		return cursorElem{"x", base64.StdEncoding.EncodeToString(x)}, nil
	}
	rv := reflect.ValueOf(v)
	// A nullable key scans into a pointer or a driver.Valuer (sql.Null*, Opt);
	// unwrap it to the underlying value, or a null element when absent.
	if rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return cursorElem{T: "0"}, nil
		}
		return toCursorElem(rv.Elem().Interface())
	}
	if valuer, ok := v.(driver.Valuer); ok {
		dv, err := valuer.Value()
		if err != nil {
			return cursorElem{}, fmt.Errorf("filtrx: cursor value: %w", err)
		}
		if dv == nil {
			return cursorElem{T: "0"}, nil
		}
		return toCursorElem(dv)
	}
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return cursorElem{"i", rv.Int()}, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return cursorElem{"u", rv.Uint()}, nil
	case reflect.Float32, reflect.Float64:
		return cursorElem{"f", rv.Float()}, nil
	case reflect.String:
		return cursorElem{"s", rv.String()}, nil
	case reflect.Bool:
		return cursorElem{"b", rv.Bool()}, nil
	default:
		return cursorElem{}, fmt.Errorf("filtrx: cannot encode keyset value of type %T into a cursor", v)
	}
}

// decodeCursor unpacks an opaque token into typed boundary values ready to bind.
// Numbers are decoded with UseNumber so a large 64-bit key (a Snowflake id, say)
// survives the round trip exactly rather than being mangled through float64.
func decodeCursor(c Cursor) ([]any, error) {
	raw, err := base64.RawURLEncoding.DecodeString(string(c))
	if err != nil {
		return nil, fmt.Errorf("filtrx: invalid cursor: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var elems []cursorElem
	if err := dec.Decode(&elems); err != nil {
		return nil, fmt.Errorf("filtrx: invalid cursor: %w", err)
	}
	vals := make([]any, len(elems))
	for i, e := range elems {
		v, err := fromCursorElem(e)
		if err != nil {
			return nil, err
		}
		vals[i] = v
	}
	return vals, nil
}

func fromCursorElem(e cursorElem) (any, error) {
	switch e.T {
	case "0":
		return nil, nil
	case "i":
		num, ok := e.V.(json.Number)
		if !ok {
			return nil, badCursorVal(e)
		}
		n, err := num.Int64()
		if err != nil {
			return nil, badCursorVal(e)
		}
		return n, nil
	case "u":
		num, ok := e.V.(json.Number)
		if !ok {
			return nil, badCursorVal(e)
		}
		n, err := strconv.ParseUint(num.String(), 10, 64)
		if err != nil {
			return nil, badCursorVal(e)
		}
		return n, nil
	case "f":
		num, ok := e.V.(json.Number)
		if !ok {
			return nil, badCursorVal(e)
		}
		n, err := num.Float64()
		if err != nil {
			return nil, badCursorVal(e)
		}
		return n, nil
	case "s":
		s, ok := e.V.(string)
		if !ok {
			return nil, badCursorVal(e)
		}
		return s, nil
	case "b":
		b, ok := e.V.(bool)
		if !ok {
			return nil, badCursorVal(e)
		}
		return b, nil
	case "t":
		s, ok := e.V.(string)
		if !ok {
			return nil, badCursorVal(e)
		}
		t, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			return nil, fmt.Errorf("filtrx: invalid cursor time: %w", err)
		}
		return t, nil
	case "x":
		s, ok := e.V.(string)
		if !ok {
			return nil, badCursorVal(e)
		}
		b, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			return nil, fmt.Errorf("filtrx: invalid cursor bytes: %w", err)
		}
		return b, nil
	default:
		return nil, fmt.Errorf("filtrx: unknown cursor value tag %q", e.T)
	}
}

func badCursorVal(e cursorElem) error {
	return fmt.Errorf("filtrx: cursor value %v does not match its type tag %q", e.V, e.T)
}
