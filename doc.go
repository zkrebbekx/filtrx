// Package filtrx turns annotated Go structs into safe, dynamic SQL WHERE clauses
// and resolves Relay-style pagination, built to sit on top of jmoiron/sqlx.
//
// The design goal is that a single filter struct, decorated once, can be filled
// straight from a REST query string or a GraphQL input object and then handed to
// the database with no further wiring. Because every legal column and operator
// is fixed by the struct's types and tags, request data can only supply values —
// never SQL — so dynamic filtering is safe by construction.
//
// # Filtering
//
// Declare a filter struct using the holder types Range, Match and Text, or plain
// Opt[T] fields with an op tag:
//
//	type UserFilter struct {
//		Status filtrx.Text         `col:"status"`
//		Name   filtrx.Text         `col:"name"`
//		Age    filtrx.Range[int]   `col:"age"`
//		Active filtrx.Opt[bool]    `col:"active" op:"eq"`
//		Roles  []string            `col:"role"  op:"in"`
//		Any    []UserFilter        `group:"or"`
//	}
//
// Compile it to a condition tree and render dialect-specific SQL:
//
//	cond, _ := filtrx.Where(f)
//	sql, args := filtrx.Build(cond, filtrx.Postgres)
//
// # Joins
//
// A filter struct may declare its FROM table and joins with Table and Join
// marker fields; filter columns then reference the aliases (col:"u.status").
// Start such a query with For, which reads the source from the struct. For a
// one-to-many relationship use an Exists field, which compiles to a correlated
// EXISTS subquery and so filters the base table without fanning the result out.
//
// # Pagination
//
// Paginate resolves First/Last/Before/After into a limit and offset and reports
// whether a pre-count is required; see Paginate and PageInfo. For deep pages,
// Query.Seek switches to keyset (cursor) pagination over the query's OrderBy
// columns, whose per-page cost does not grow with depth; see SeekParams and
// Cursor. A nullable key column needs an explicit NULL placement via
// OrderByNulls. ListConnection assembles a keyset page into a GraphQL Relay
// Connection, and BindSeek reads its arguments from a query string.
//
// The FullText holder compiles to the dialect's native full-text match
// (Postgres websearch_to_tsquery, MySQL MATCH/AGAINST, SQLite FTS5) instead of a
// LIKE scan.
//
// # Grouping
//
// Query.GroupBy and Query.Having add aggregate queries; the fast total then
// counts groups, and Count wraps the grouped result, so pagination stays exact.
//
// # Updates and deletes
//
// The same filter can drive a write: Query.Delete and Query.Update reuse the
// compiled WHERE. Both refuse an empty filter unless Query.Unfiltered authorises
// a whole-table mutation.
//
// # One-call listing
//
// List combines filtering, pagination, the COUNT(*) OVER() fast total and sqlx
// scanning into a single call returning the records and a PageInfo.
package filtrx
