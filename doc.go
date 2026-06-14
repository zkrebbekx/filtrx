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
// # Pagination
//
// Paginate resolves First/Last/Before/After into a limit and offset and reports
// whether a pre-count is required; see Paginate and PageInfo.
//
// # One-call listing
//
// List combines filtering, pagination, the COUNT(*) OVER() fast total and sqlx
// scanning into a single call returning the records and a PageInfo.
package filtrx
