# Announcing filtrx: struct-driven WHERE clauses and pagination for sqlx

Go has good query builders (squirrel, goqu) and a scattering of paginators. What
it has *not* had is one library that ties **type-safe dynamic filtering**,
**nested AND/OR**, **offset _and_ keyset pagination**, and **GraphQL Relay
connections** together behind a single decorated struct — safe by construction,
filled straight from the wire, on Postgres, MySQL or SQLite.

That's [filtrx](https://github.com/zkrebbekx/filtrx). Its only runtime dependency
is sqlx.

## Decorate once, fill from the wire

A filter struct is a contract. Its field *types* and *tags* fix the legal columns
and operators, so request data can only ever supply values — there is no string
concatenation of user input anywhere in the core.

```go
type UserFilter struct {
	Status filtrx.Text       `col:"status"`
	Age    filtrx.Range[int] `col:"age"`
	Roles  []string          `col:"role" op:"in"`
	Any    []UserFilter      `group:"or"`
}
```

```json
{ "status": { "eq": "active" }, "age": { "gte": 18, "lt": 65 }, "roles": ["admin","mod"] }
```

unmarshals straight onto `UserFilter` (`Opt[T]` gives exact JSON presence — a
missing key is unset, a present zero is a real filter) and compiles to:

```sql
WHERE ("status" = $1 AND "age" >= $2 AND "age" < $3 AND "role" IN ($4, $5))
```

`Bind`/`BindPage`/`BindSeek` do the same from a REST query string.

## One query for the page *and* the total

`List` rides `COUNT(*) OVER()` alongside the page, so the total comes back in the
same statement — a second `COUNT` runs only when a page is empty.

## Pagination that doesn't slow down

Offset paging is there (`First/Last/After/Before`), but page 10,000 re-scans
10,000 rows. **Keyset paging** seeks straight past the last row, with flat
per-page cost:

```go
q := filtrx.From("events").OrderByDesc("created_at").OrderBy("id").
	Seek(filtrx.SeekParams{Size: 20})
conn, _ := filtrx.ListConnection[Event](ctx, db, q) // Relay edges + pageInfo
```

Cursors are opaque and **type-tagged**, so a 64-bit id survives the round trip
exactly (no `float64` mangling), and the seek predicate expands to a portable
lexicographic `OR`-of-`AND` — correct for **mixed ASC/DESC** ordering and even
nullable keys (`OrderByNulls`), across all three databases.

## Relationships without fan-out

A one-to-many `JOIN` duplicates base rows and corrupts your page total. filtrx's
`Exists[T]` compiles to a correlated `EXISTS` instead — filter customers by their
*many* orders without multiplying anyone:

```go
type CustomerFilter struct {
	Base   filtrx.Table            `table:"customers" as:"c"`
	Orders filtrx.Exists[OrderSub] `exists:"orders o" on:"o.customer_id = c.id"`
}
```

## Grouping, too

`GroupBy` + `Having` bring aggregate queries under the same filter/page/total
machinery — the window total counts groups, and `Count` wraps the grouped result,
so pagination stays accurate.

## Try it

```bash
go get github.com/zkrebbekx/filtrx
```

Everything above is verified by a portable integration suite that runs against
SQLite, PostgreSQL 16 and MySQL 8. Docs and examples:
<https://pkg.go.dev/github.com/zkrebbekx/filtrx>.
