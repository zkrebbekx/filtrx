# filtrx

[![CI](https://github.com/zkrebbekx/filtrx/actions/workflows/ci.yml/badge.svg)](https://github.com/zkrebbekx/filtrx/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/zkrebbekx/filtrx/branch/main/graph/badge.svg)](https://codecov.io/gh/zkrebbekx/filtrx)
[![Go Reference](https://pkg.go.dev/badge/github.com/zkrebbekx/filtrx.svg)](https://pkg.go.dev/github.com/zkrebbekx/filtrx)
[![Go Report Card](https://goreportcard.com/badge/github.com/zkrebbekx/filtrx)](https://goreportcard.com/report/github.com/zkrebbekx/filtrx)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

**Struct-driven `WHERE` clauses and Relay-style pagination for
[sqlx](https://github.com/jmoiron/sqlx).**

Decorate a struct once. Fill it straight from a REST query string or a GraphQL
input object. Hand it to the database. filtrx builds the dynamic `WHERE`, runs
keyset-free offset pagination, and returns the total — in a single query, on
Postgres, MySQL or SQLite.

> **Status:** pre-1.0, actively developed. The only runtime dependency is sqlx.

## Why

Go has good query builders (squirrel, goqu) and basic paginators, but nothing
that ties **type-safe dynamic filtering**, **nested AND/OR**, and
**`first`/`last`/`total`/`hasNextPage` pagination** into one safe, struct-first
API. filtrx is that missing piece:

- **Safe by construction.** Every column and operator is fixed by the filter
  struct's types and tags. Request data only ever supplies *values*, so dynamic
  filtering can't become SQL injection — there is no string concatenation in the
  core at all.
- **Fill from the wire.** `Opt[T]` gives exact JSON presence detection (a missing
  key is unset, a present zero is a real filter), so request bodies map straight
  onto filter structs.
- **One query for the total.** `COUNT(*) OVER()` rides along with the page; a
  second `COUNT` runs only when a page is empty.
- **Portable.** The same struct renders correct SQL for Postgres (`$1`, `"id"`),
  MySQL (`?`, `` `id` ``) and SQLite — verified by an integration suite that runs
  against all three.

## Install

```bash
go get github.com/zkrebbekx/filtrx
```

## Quick start

```go
// Decorate once. This struct is your filter contract.
type UserFilter struct {
	Status filtrx.Text       `col:"status"`
	Name   filtrx.Text       `col:"name"`
	Age    filtrx.Range[int] `col:"age"`
	Roles  []string          `col:"role" op:"in"`
	Any    []UserFilter      `group:"or"`
}

type User struct {
	ID   int    `db:"id"`
	Name string `db:"name"`
}

func listUsers(ctx context.Context, db *sqlx.DB, f UserFilter) ([]User, filtrx.PageInfo, error) {
	var users []User
	q := filtrx.From("users").
		Where(f).
		OrderBy("id").
		Page(filtrx.PagingParams{First: ptr(20), IncludeTotal: true}).
		On(filtrx.Postgres)

	info, err := filtrx.List(ctx, db, q, &users)
	return users, info, err
}
```

A request like:

```json
{ "status": { "eq": "active" }, "age": { "gte": 18, "lt": 65 }, "roles": ["admin","mod"] }
```

unmarshals straight into `UserFilter` and produces:

```sql
SELECT *, COUNT(*) OVER() AS _filtrx_total
FROM "users"
WHERE ("status" = $1 AND "age" >= $2 AND "age" < $3 AND "role" IN ($4, $5))
ORDER BY "id"
LIMIT $6 OFFSET $7
-- args: active, 18, 65, admin, mod, 21, 0
```

## From a REST handler

`Bind`, `BindPage` and `Sort` turn a raw query string into a filter, paging and
ordering — no manual parsing, and the column allow-list keeps sort safe:

```go
func listUsers(w http.ResponseWriter, r *http.Request) {
	v := r.URL.Query() // ?status=active&age_gte=18&role=admin&first=20&total=true&sort=-created

	var f UserFilter
	if err := filtrx.Bind(v, &f); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	page, err := filtrx.BindPage(v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var users []User
	q := filtrx.From("users").
		Where(f).
		Sort(v.Get("sort"), map[string]string{ // only these keys may sort
			"name":    "name",
			"created": "created_at",
		}).
		Page(page)

	info, err := filtrx.List(r.Context(), db, q, &users)
	// ... encode {users, info} as JSON
}
```

Query-parameter conventions: `status=active` (equality), `age_gte=18&age_lt=65`
(range), `role=admin&role=mod` or `role=admin,mod` (IN), `active=true` (scalar
`Opt`). Pagination: `first` / `last` / `after` / `before` / `total`. Unknown
parameters are ignored, so everything coexists in one query string.

## Filtering

### Holders

A filter field's type declares which operators that column allows.

| Holder          | Operators                                  | For                          |
| --------------- | ------------------------------------------ | ---------------------------- |
| `Range[T]`      | `eq ne gt gte lt lte in null`              | ordered columns (int, time…) |
| `Match[T]`      | `eq ne in null`                            | enums, UUIDs, bools          |
| `Text`          | `eq ne like ilike in null`                 | strings                      |
| `Opt[T]` + `op` | one operator, from the `op` tag            | a single fixed predicate     |
| `[]T`           | `in` (or `nin`)                            | membership                   |

```go
type ProductFilter struct {
	SKU      filtrx.Text          `col:"sku"`
	Price    filtrx.Range[int]    `col:"price_cents"`
	Status   filtrx.Match[string] `col:"status"`
	Featured filtrx.Opt[bool]     `col:"featured" op:"eq"`
	Tags     []string             `col:"tag" op:"in"`
}
```

### Nesting

`group:"and"` / `group:"or"` fields hold child filters and bracket exactly:

```go
type ProductFilter struct {
	InStock filtrx.Opt[bool] `col:"in_stock" op:"eq"`
	Any     []ProductFilter  `group:"or"`
}
// InStock=true, Any=[{category=tools}, {price>4000}]  →
// ("in_stock" = $1 AND ("category" = $2 OR "price_cents" > $3))
```

### Tags

| Tag            | Meaning                                                        |
| -------------- | ------------------------------------------------------------- |
| `col:"name"`   | SQL column. Falls back to the `db` tag, then snake_case.      |
| `op:"gte"`     | Operator for an `Opt`/slice field (default `eq`/`in`).        |
| `group:"or"`   | Marks a slice-of-struct field as an OR (or `and`) group.      |
| `col:"-"`      | Skip the field.                                               |

Operator words: `eq ne gt gte lt lte like ilike in nin null nnull`.

### The builder API

`Where` compiles to a `Cond` tree you can also build by hand — handy for the
predicates a struct can't express:

```go
cond := filtrx.And(
	filtrx.Eq("status", "active"),
	filtrx.Or(filtrx.Gt("age", 18), filtrx.IsNull("deleted_at")),
	filtrx.Raw("tags @> ?", pq.Array([]string{"go"})), // escape hatch
)
sql, args := filtrx.Build(cond, filtrx.Postgres)
```

## Joins

Joins are declared in the filter struct too, with `filtrx.Table` and
`filtrx.Join` marker fields. Filter columns then reference the aliases. Start the
query with `For`, which reads the table and joins from the struct:

```go
type OrderFilter struct {
	Base   filtrx.Table `table:"users"  as:"u"`
	Orders filtrx.Join  `table:"orders" as:"o" on:"o.user_id = u.id" type:"left"`

	Status filtrx.Text       `col:"u.status"`
	Total  filtrx.Range[int] `col:"o.total"`
}

q := filtrx.For(OrderFilter{
	Status: filtrx.Text{Eq: filtrx.Some("active")},
	Total:  filtrx.Range[int]{Gt: filtrx.Some(100)},
}).OrderBy("u.id").Page(page)

info, err := filtrx.List(ctx, db, q, &users)
```
→
```sql
SELECT "u".* FROM "users" "u"
LEFT JOIN "orders" "o" ON o.user_id = u.id
WHERE ("u"."status" = $1 AND "o"."total" > $2)
ORDER BY "u"."id" LIMIT $3 OFFSET $4
```

- `type` is `inner` (default), `left`, `right` or `full`.
- Qualified columns (`u.status`) are quoted per segment (`"u"."status"`).
- With a join, the projection defaults to the base table's columns (`"u".*`) to
  avoid pulling — and colliding on — every joined table's columns.
- `on` expressions are emitted verbatim, so never build them from request data.

> **Join cardinality matters.** filtrx joins are for filtering the base table by
> a related one where the base row is **not multiplied** — many-to-one or
> one-to-one (a user's organization, a product's category). A **one-to-many**
> join (a user's *many* orders) fans the result out: the base row repeats once
> per match, which duplicates rows in the page, inflates the `COUNT(*) OVER()`
> total, and makes `LIMIT`/offset count joined rows instead of entities. For
> one-to-many filtering use **`Exists`** (below), not a `Join`.
>
> Portability: `full` joins aren't supported by MySQL and only by recent SQLite.
> Keep table aliases lowercase — the `on` expression is emitted verbatim, so a
> quoted mixed-case alias (`as:"U"`) won't match an unquoted `U` in `on`.

### One-to-many: `Exists`

To filter the base table by its *many* side without fan-out, declare an
`Exists[T]` field. It compiles to a correlated `EXISTS` subquery, so the base
row is tested — never multiplied — and the page count stays accurate. The nested
struct `T` supplies the child-table predicates; the `exists` and `on` tags name
the subquery source and its correlation.

```go
type OrderSub struct {
	Status filtrx.Text       `col:"o.status"`
	Total  filtrx.Range[int] `col:"o.total"`
}
type CustomerFilter struct {
	Base   filtrx.Table            `table:"customers" as:"c"`
	Status filtrx.Text             `col:"c.status"`
	Orders filtrx.Exists[OrderSub] `exists:"orders o" on:"o.customer_id = c.id"`
}

f := CustomerFilter{
	Orders: filtrx.Exists[OrderSub]{
		When: filtrx.Some(true), // Some(false) → NOT EXISTS; unset → ignored
		Sub:  OrderSub{Status: filtrx.Text{Eq: filtrx.Some("paid")}},
	},
}
```
→
```sql
EXISTS (SELECT 1 FROM orders o WHERE o.customer_id = c.id AND "o"."status" = $1)
```

`When` is the request-friendly toggle: leave it unset to ignore the relationship,
`Some(true)` for `EXISTS`, `Some(false)` for `NOT EXISTS`. Like a join's `on`,
the `exists` and `on` tags are emitted verbatim — never build them from request
data. The child predicates in `Sub` are parameterised normally.

## Pagination

`PagingParams` mirrors the Relay connection arguments over record offsets:

```go
type PagingParams struct {
	Before, After *int // record offsets
	First, Last   *int // window size from the start or end
	IncludeTotal  bool
}
```

`List` returns a `PageInfo`:

```go
type PageInfo struct {
	Total     int  // matching rows (when requested)
	Offset    int  // index of the first returned row — cursor basis
	Truncated bool // more rows exist past this window
}
```

- **`First` / `After`** page forward; the total comes from `COUNT(*) OVER()` in
  the same query.
- **`Last`** pages from the end; filtrx pre-counts once to resolve the offset.
- An **empty** filtered page still reports an accurate `Total` via a fallback
  `COUNT`.

Use `Paginate` directly if you're driving your own query:

```go
paginator, needsTotal := filtrx.Paginate(params)
if needsTotal { /* SELECT COUNT(*) ... */ }
limit, offset := paginator(total)
```

### Keyset (cursor) pagination

Offset paging re-scans every skipped row, so page 10,000 is slow. **Keyset
paging** seeks straight past the last row instead — its cost is flat no matter
how deep the page. Switch a query to it with `Seek`; the `OrderBy` terms are both
the sort and the cursor key.

```go
q := filtrx.From("events").
	Where(f).
	OrderByDesc("created_at").
	OrderBy("id"). // unique tiebreaker → a total order
	Seek(filtrx.SeekParams{Size: 20})

var page []Event
info, err := filtrx.List(ctx, db, q, &page)

// info.EndCursor is the token for the next page — rebuild the same query,
// seeking after it:
q = filtrx.From("events").Where(f).OrderByDesc("created_at").OrderBy("id").
	Seek(filtrx.SeekParams{After: info.EndCursor, Size: 20})
```
→
```sql
SELECT * FROM "events"
ORDER BY "created_at" DESC, "id"
LIMIT $1            -- Size+1, to detect a following page

-- next page, After the cursor (created_at=t, id=n):
WHERE ("created_at" < $1 OR ("created_at" = $2 AND "id" > $3))
ORDER BY "created_at" DESC, "id" LIMIT $4
```

- **Cursors are opaque.** `StartCursor`/`EndCursor` on `PageInfo` are URL-safe
  tokens that encode the row's ordering values (type-tagged so an integer comes
  back an integer, a timestamp a timestamp). Pass `EndCursor` as the next page's
  `After`, or `StartCursor` as a previous page's `Before`. Don't parse them.
- **`Truncated`** means more rows exist in the paging direction — your
  `hasNextPage` (forward) or `hasPreviousPage` (backward).
- **Mixed `ASC`/`DESC` is fine.** The seek predicate expands to a portable
  lexicographic `OR`-of-`AND` rather than a row-value comparison, so directions
  can differ per column across Postgres, MySQL and SQLite.
- **Keyset key columns must be `NOT NULL`** and form a total order (end with a
  unique column). NULLs have no portable seek semantics, so they're rejected.
- Keyset doesn't compute a total by default (that's the point); set
  `IncludeTotal` to pay one extra `COUNT`.

## Dialects

`filtrx.Postgres` (default), `filtrx.MySQL`, `filtrx.SQLite` — set with
`Query.On(...)` or pass to `Build`. They differ in placeholders, identifier
quoting and the unbounded-`OFFSET` form; the same filter struct works across all
three.

## Testing locally

```bash
make test         # unit tests (race)
make integration  # SQLite always; Postgres + MySQL via Docker (testcontainers)
```

## License

[MIT](LICENSE) © 2026 Zac Krebbekx
