package filtrx

import (
	"reflect"
	"strings"
)

// Cond is a node in a SQL boolean expression: either a leaf comparison or a
// nested And/Or group. Groups render with parentheses, so arbitrary nesting and
// operator precedence are always exact — there is no string concatenation to
// get wrong. Build a Cond by hand with the constructors below, or let Where
// compile one from a tagged struct.
type Cond interface {
	write(b *builder)
}

// builder accumulates SQL text and bind arguments for one render pass. The
// dialect supplies placeholders and identifier quoting; n tracks the 1-based
// argument position.
type builder struct {
	sql  strings.Builder
	args []any
	d    Dialect
	n    int
}

func (b *builder) bind(v any) string {
	b.n++
	b.args = append(b.args, v)
	return b.d.placeholder(b.n)
}

// Build renders a condition tree into a WHERE fragment (without the leading
// WHERE keyword) and its ordered bind arguments, using d for placeholders and
// identifier quoting. A nil Cond yields an empty string and no arguments.
func Build(c Cond, d Dialect) (sql string, args []any) {
	if c == nil {
		return "", nil
	}
	b := &builder{d: d}
	c.write(b)
	return b.sql.String(), b.args
}

// Comparison operators. The tag word on the left of each mapping (see opWords)
// is what users write in `op:"..."` struct tags; the value is the SQL emitted.
type op string

const (
	opEq    op = "="
	opNe    op = "<>"
	opGt    op = ">"
	opLt    op = "<"
	opGte   op = ">="
	opLte   op = "<="
	opLike  op = "LIKE"
	opILike op = "ILIKE"
	opIn    op = "IN"
	opNotIn op = "NOT IN"
	opNull  op = "IS NULL"
	opNNull op = "IS NOT NULL"
)

// opWords maps the words accepted in `op:"..."` tags to operators.
var opWords = map[string]op{
	"eq":    opEq,
	"ne":    opNe,
	"gt":    opGt,
	"lt":    opLt,
	"gte":   opGte,
	"lte":   opLte,
	"like":  opLike,
	"ilike": opILike,
	"in":    opIn,
	"nin":   opNotIn,
	"null":  opNull,
	"nnull": opNNull,
}

// cmp is a leaf comparison: column op value. For opIn/opNotIn the value is a
// slice expanded to a parenthesised placeholder list. For opNull/opNNull there
// is no value.
type leaf struct {
	col string
	op  op
	val any
}

func (c leaf) write(b *builder) {
	b.sql.WriteString(b.d.quoteIdent(c.col))
	switch c.op {
	case opNull, opNNull:
		b.sql.WriteByte(' ')
		b.sql.WriteString(string(c.op))
	case opIn, opNotIn:
		b.sql.WriteByte(' ')
		b.sql.WriteString(string(c.op))
		b.sql.WriteString(" (")
		b.writeList(c.val)
		b.sql.WriteByte(')')
	default:
		b.sql.WriteByte(' ')
		b.sql.WriteString(string(c.op))
		b.sql.WriteByte(' ')
		b.sql.WriteString(b.bind(c.val))
	}
}

// writeList expands a slice value into "?, ?, ?" with one bound argument per
// element. A non-slice value is bound as a single element. An empty slice would
// produce invalid SQL, so callers guard against it before constructing the cmp.
func (b *builder) writeList(val any) {
	rv := reflect.ValueOf(val)
	if rv.Kind() != reflect.Slice {
		b.sql.WriteString(b.bind(val))
		return
	}
	for i := 0; i < rv.Len(); i++ {
		if i > 0 {
			b.sql.WriteString(", ")
		}
		b.sql.WriteString(b.bind(rv.Index(i).Interface()))
	}
}

// group is a parenthesised And/Or join of child conditions.
type group struct {
	or    bool
	conds []Cond
}

func (g group) write(b *builder) {
	if len(g.conds) == 0 {
		return
	}
	if len(g.conds) == 1 {
		g.conds[0].write(b) // no redundant parentheses around a single child
		return
	}
	joiner := " AND "
	if g.or {
		joiner = " OR "
	}
	b.sql.WriteByte('(')
	for i, c := range g.conds {
		if i > 0 {
			b.sql.WriteString(joiner)
		}
		c.write(b)
	}
	b.sql.WriteByte(')')
}

// raw is an escape hatch: literal SQL with its own bind arguments, spliced into
// the tree. The caller owns its safety — only the fixed sql string is emitted,
// never user input, and args fill its placeholders in order.
type raw struct {
	sql  string
	args []any
}

func (r raw) write(b *builder) {
	// Re-render the fragment so its placeholders follow the surrounding
	// numbering; "?" markers are replaced positionally from r.args.
	parts := strings.Split(r.sql, "?")
	for i, p := range parts {
		b.sql.WriteString(p)
		if i < len(parts)-1 && i < len(r.args) {
			b.sql.WriteString(b.bind(r.args[i]))
		}
	}
}

// And joins conditions with AND inside parentheses. Nil children are dropped, so
// conditionally-built slices stay clean. With a single effective child the
// parentheses are omitted.
func And(conds ...Cond) Cond { return group{or: false, conds: compact(conds)} }

// Or joins conditions with OR inside parentheses, dropping nil children.
func Or(conds ...Cond) Cond { return group{or: true, conds: compact(conds)} }

// Eq builds "col = val".
func Eq(col string, val any) Cond { return leaf{col, opEq, val} }

// Ne builds "col <> val".
func Ne(col string, val any) Cond { return leaf{col, opNe, val} }

// Gt builds "col > val".
func Gt(col string, val any) Cond { return leaf{col, opGt, val} }

// Lt builds "col < val".
func Lt(col string, val any) Cond { return leaf{col, opLt, val} }

// Gte builds "col >= val".
func Gte(col string, val any) Cond { return leaf{col, opGte, val} }

// Lte builds "col <= val".
func Lte(col string, val any) Cond { return leaf{col, opLte, val} }

// Like builds "col LIKE val".
func Like(col string, val any) Cond { return leaf{col, opLike, val} }

// In builds "col IN (...)" from a slice value.
func In(col string, vals any) Cond { return leaf{col, opIn, vals} }

// NotIn builds "col NOT IN (...)" from a slice value.
func NotIn(col string, vals any) Cond { return leaf{col, opNotIn, vals} }

// IsNull builds "col IS NULL".
func IsNull(col string) Cond { return leaf{col, opNull, nil} }

// IsNotNull builds "col IS NOT NULL".
func IsNotNull(col string) Cond { return leaf{col, opNNull, nil} }

// Raw splices a literal SQL fragment and its arguments into the tree. Use it for
// the rare predicate filtrx cannot express (a function call, a subquery). The
// fragment is emitted verbatim, so never build it from untrusted input.
func Raw(sql string, args ...any) Cond { return raw{sql: sql, args: args} }

// compact drops nil children so callers can append conditionally.
func compact(conds []Cond) []Cond {
	out := conds[:0]
	for _, c := range conds {
		if c != nil {
			out = append(out, c)
		}
	}
	return out
}
