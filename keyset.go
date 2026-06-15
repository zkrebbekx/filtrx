package filtrx

import (
	"bytes"
	"context"
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

// listKeyset runs a Query in keyset mode: it seeks past the cursor (if any),
// fetches one extra row to detect a following page, restores natural order for a
// backward page, and emits the start/end cursors for the returned window.
func listKeyset[T any](ctx context.Context, db sqlx.QueryerContext, q *Query, dest *[]T) (PageInfo, error) {
	if len(q.order) == 0 {
		return PageInfo{}, fmt.Errorf("%w: keyset pagination requires at least one OrderBy", ErrCompile)
	}
	if q.seek.size <= 0 {
		return PageInfo{}, fmt.Errorf("%w: Seek size must be positive", ErrCompile)
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
			return PageInfo{}, fmt.Errorf("%w: keyset order column %q has no matching field on %s", ErrCompile, q.order[i].col, rowType)
		}
	}

	// Seek predicate from the cursor, AND-ed onto the filter. The first page has
	// no cursor and selects from the start of the order.
	cond := q.cond
	if q.seek.cursor != "" {
		vals, err := decodeCursor(q.seek.cursor)
		if err != nil {
			return PageInfo{}, fmt.Errorf("%w: %w", ErrCompile, err)
		}
		if len(vals) != len(q.order) {
			return PageInfo{}, fmt.Errorf("%w: cursor carries %d values but the query orders by %d columns", ErrCompile, len(vals), len(q.order))
		}
		cond = And(q.cond, keysetCond(q.order, vals, q.seek.before))
	}
	where, whereArgs := Build(cond, q.dialect)

	// A backward page is fetched in reversed order (so the rows nearest the cursor
	// come first under LIMIT) and flipped back to natural order before returning.
	order := q.order
	if q.seek.before {
		order = make([]orderTerm, len(q.order))
		for i, o := range q.order {
			order[i] = orderTerm{col: o.col, desc: !o.desc}
		}
	}

	query, args := q.buildKeysetSelect(where, whereArgs, order, q.seek.size+1)
	rows, err := db.QueryxContext(ctx, query, args...)
	if err != nil {
		return PageInfo{}, fmt.Errorf("%w: %w", ErrQuery, err)
	}
	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	if err != nil {
		return PageInfo{}, fmt.Errorf("%w: columns: %w", ErrQuery, err)
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
			return PageInfo{}, fmt.Errorf("%w: scan: %w", ErrQuery, err)
		}
		key := make([]any, len(keyTraversals))
		for i, tr := range keyTraversals {
			key[i] = reflectx.FieldByIndexes(rv, tr).Interface()
		}
		*dest = append(*dest, row)
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return PageInfo{}, fmt.Errorf("%w: %w", ErrQuery, err)
	}

	var info PageInfo
	// The extra row reveals a further page in the paging direction; drop it.
	if len(keys) > q.seek.size {
		info.Truncated = true
		*dest = (*dest)[:start+q.seek.size]
		keys = keys[:q.seek.size]
	}
	// Restore natural order for a backward page.
	if q.seek.before {
		reverse((*dest)[start:])
		reverse(keys)
	}

	if n := len(keys); n > 0 {
		if info.StartCursor, err = encodeCursor(keys[0]); err != nil {
			return PageInfo{}, err
		}
		if info.EndCursor, err = encodeCursor(keys[n-1]); err != nil {
			return PageInfo{}, err
		}
	}

	if q.seek.total {
		baseWhere, baseArgs := Build(q.cond, q.dialect)
		n, cerr := count(ctx, db, q, baseWhere, baseArgs)
		if cerr != nil {
			return PageInfo{}, cerr
		}
		info.Total = n
	}
	return info, nil
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
	q.writeOrder(&sb, order)
	sb.WriteString(" LIMIT ")
	sb.WriteString(q.dialect.placeholder(len(whereArgs) + 1))
	return sb.String(), append(whereArgs, limit)
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
func keysetCond(order []orderTerm, vals []any, before bool) Cond {
	branches := make([]Cond, len(order))
	for i := range order {
		ands := make([]Cond, 0, i+1)
		for j := 0; j < i; j++ {
			ands = append(ands, Eq(order[j].col, vals[j]))
		}
		ascending := order[i].desc == before // desc flips it; before flips again
		if ascending {
			ands = append(ands, Gt(order[i].col, vals[i]))
		} else {
			ands = append(ands, Lt(order[i].col, vals[i]))
		}
		branches[i] = And(ands...)
	}
	return Or(branches...)
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

// encodeCursor packs boundary values into an opaque URL-safe token. A NULL value
// is rejected: keyset columns must be NOT NULL for the seek predicate to be
// correct and portable.
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
		return cursorElem{}, fmt.Errorf("filtrx: keyset order column is NULL; keyset columns must be NOT NULL")
	case time.Time:
		return cursorElem{"t", x.Format(time.RFC3339Nano)}, nil
	case []byte:
		return cursorElem{"x", base64.StdEncoding.EncodeToString(x)}, nil
	}
	rv := reflect.ValueOf(v)
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
