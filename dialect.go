package filtrx

import (
	"strconv"
	"strings"
)

// Dialect captures the SQL differences filtrx needs to emit portable queries:
// how bind placeholders are written and how identifiers are quoted. The three
// built-in dialects cover the databases sqlx is most used with.
type Dialect interface {
	// placeholder returns the bind marker for the 1-based argument position n.
	// Postgres returns "$n"; MySQL and SQLite return "?".
	placeholder(n int) string
	// quoteIdent quotes a column or table identifier for the dialect.
	quoteIdent(s string) string
	// supportsWindowCount reports whether COUNT(*) OVER() can be used to obtain
	// the total in the same query (the "fast total" path).
	supportsWindowCount() bool
	// allRowsLimit returns the LIMIT value meaning "no upper bound", used when an
	// OFFSET is required without a row limit. Postgres returns "" (OFFSET stands
	// alone); SQLite returns "-1"; MySQL returns its maximum row count.
	allRowsLimit() string
	// supportsNullsOrdering reports whether the dialect accepts the standard
	// "NULLS FIRST"/"NULLS LAST" suffix on an ORDER BY term. Postgres and modern
	// SQLite do; MySQL does not and must emulate it with a leading ISNULL() key.
	supportsNullsOrdering() bool
	// fullTextMatch renders a full-text match of the already-quoted column against
	// the search string bound at placeholder. config is the Postgres text-search
	// configuration (ignored elsewhere). The search string is always a bind
	// parameter; only the column and config reach the SQL text.
	fullTextMatch(quotedCol, config, placeholder string) string
}

// Postgres targets PostgreSQL: numbered $1 placeholders and double-quoted
// identifiers.
var Postgres Dialect = postgres{}

// MySQL targets MySQL and MariaDB: ? placeholders and backtick identifiers.
var MySQL Dialect = mysql{}

// SQLite targets SQLite: ? placeholders and double-quoted identifiers.
var SQLite Dialect = sqlite{}

type postgres struct{}

func (postgres) placeholder(n int) string    { return "$" + strconv.Itoa(n) }
func (postgres) quoteIdent(s string) string  { return quoteName(s, '"') }
func (postgres) supportsWindowCount() bool   { return true }
func (postgres) allRowsLimit() string        { return "" }
func (postgres) supportsNullsOrdering() bool { return true }
func (postgres) fullTextMatch(col, config, ph string) string {
	// websearch_to_tsquery accepts free-form user input without ever erroring on
	// syntax, so the search string is safe to pass straight through.
	return col + " @@ websearch_to_tsquery('" + config + "', " + ph + ")"
}

type mysql struct{}

func (mysql) placeholder(int) string      { return "?" }
func (mysql) quoteIdent(s string) string  { return quoteName(s, '`') }
func (mysql) supportsWindowCount() bool   { return true }
func (mysql) allRowsLimit() string        { return "18446744073709551615" }
func (mysql) supportsNullsOrdering() bool { return false }
func (mysql) fullTextMatch(col, _, ph string) string {
	return "MATCH(" + col + ") AGAINST(" + ph + " IN NATURAL LANGUAGE MODE)"
}

type sqlite struct{}

func (sqlite) placeholder(int) string      { return "?" }
func (sqlite) quoteIdent(s string) string  { return quoteName(s, '"') }
func (sqlite) allRowsLimit() string        { return "-1" }
func (sqlite) supportsWindowCount() bool   { return true }
func (sqlite) supportsNullsOrdering() bool { return true }
func (sqlite) fullTextMatch(col, _, ph string) string {
	// SQLite FTS5: the column (an FTS5 table/column) matches the query directly.
	return col + " MATCH " + ph
}

// orderClause renders one ORDER BY term: the quoted column, direction, and any
// explicit NULL placement. Dialects with native NULLS FIRST/LAST get the suffix;
// MySQL emulates it with a leading ISNULL() sort key (ISNULL is 1 for NULL, so
// "ISNULL(col) DESC" sorts NULLs first, "ISNULL(col)" sorts them last).
func orderClause(d Dialect, quoted string, desc bool, nulls nullsPos) string {
	dir := ""
	if desc {
		dir = " DESC"
	}
	if nulls == nullsDefault {
		return quoted + dir
	}
	if d.supportsNullsOrdering() {
		if nulls == nullsFirst {
			return quoted + dir + " NULLS FIRST"
		}
		return quoted + dir + " NULLS LAST"
	}
	isnull := "ISNULL(" + quoted + ")"
	if nulls == nullsFirst {
		return isnull + " DESC, " + quoted + dir
	}
	return isnull + ", " + quoted + dir
}

// quoteName quotes a possibly-qualified identifier. A dotted name like
// "u.status" is quoted segment by segment ("u"."status") so a table or alias
// qualifier survives joins; an unqualified name is quoted whole.
func quoteName(s string, q byte) string {
	if !strings.Contains(s, ".") {
		return quote(s, q)
	}
	parts := strings.Split(s, ".")
	for i, p := range parts {
		// Leave a wildcard or empty segment alone: "u.*" must stay "u".* (a
		// quoted "*" would be a literal column named *), and an empty segment
		// has nothing to quote.
		if p == "" || p == "*" {
			continue
		}
		parts[i] = quote(p, q)
	}
	return strings.Join(parts, ".")
}

// quote wraps s in the given quote rune, doubling any embedded quote so an
// identifier can never break out of its quoting. filtrx only ever quotes
// identifiers drawn from struct tags, but defending here keeps that guarantee
// local rather than relying on every call site.
func quote(s string, q byte) string {
	out := make([]byte, 0, len(s)+2)
	out = append(out, q)
	for i := 0; i < len(s); i++ {
		if s[i] == q {
			out = append(out, q)
		}
		out = append(out, s[i])
	}
	out = append(out, q)
	return string(out)
}
