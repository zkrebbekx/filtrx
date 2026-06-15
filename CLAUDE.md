# CLAUDE.md

Guidance for AI assistants and contributors working in this repository.

## What this is

`filtrx` turns annotated Go structs into safe, dynamic SQL `WHERE` clauses and
resolves Relay-style pagination, built to sit on top of
[jmoiron/sqlx](https://github.com/jmoiron/sqlx). One filter struct, decorated
once, can be filled straight from a REST query string or a GraphQL input object
and handed to the database with no further wiring — and because every legal
column and operator is fixed by the struct's types and tags, request data can
only supply values, never SQL.

## Architecture

A small, layered library. Each layer is usable on its own; `List` ties them
together.

```
tagged struct ──Where──▶ Cond tree ──Build(Dialect)──▶ SQL + args
PagingParams ──Paginate──▶ limit/offset + needsTotal
                                  └──────────── List[T] ───────────▶ []T + PageInfo
```

| File          | Responsibility                                                        |
| ------------- | -------------------------------------------------------------------- |
| `opt.go`      | `Opt[T]`, the no-pointer optional with exact JSON presence.          |
| `cond.go`     | The `Cond` predicate tree, `Build`, and the `And/Or/Eq/...` builders. |
| `dialect.go`  | `Dialect`: placeholders, identifier quoting, "all rows" limit.       |
| `holders.go`  | Generic filter holders `Range[T]`, `Match[T]`, `Text`; `Predicate`.  |
| `fulltext.go` | `FullText` holder → dialect-native full-text match.                  |
| `exists.go`   | `Exists[T]` holder → correlated `EXISTS` for one-to-many filtering.   |
| `mutate.go`   | `Query.Delete`/`Update`/`Unfiltered`: filter-driven writes.          |
| `plan.go`     | Reflection: compile a tagged struct to a `Cond`, cached per type.    |
| `paginate.go` | `PagingParams`, `PageInfo`, `Paginate`, `TruncateAt`.                |
| `keyset.go`   | `Seek`/`SeekParams`, opaque `Cursor`, keyset (seek) pagination.      |
| `connection.go`| `Connection[T]`/`ListConnection`: GraphQL Relay edges + page info.   |
| `query.go`    | `Query` builder + `List[T]`: filter + page + group + fast total + scan.|
| `integration/`| Separate module; the same BDD suite run against SQLite/Postgres/MySQL.|

The predicate tree is the safe core. Everything that accepts user input
(`Where`, the holders) compiles *down* to it; nothing concatenates SQL strings.
Keep that direction: new input surfaces should produce `Cond`, never raw SQL.

Heavy test-only dependencies (testcontainers, DB drivers) live in the nested
`integration` module so the published library stays lean — its only runtime
dependency is sqlx. Keep it that way.

## Commands

```bash
make test         # go test -race ./...
make lint         # golangci-lint
make cover        # HTML coverage report
make integration  # real-database suite (needs Docker)
```

Always run `make lint test` before committing.

## Coding standards

Follow the Go standard library style ([Effective Go](https://go.dev/doc/effective_go),
[Google Go Style Guide](https://google.github.io/styleguide/go/)).

### Comments

- **Document every exported identifier** with a doc comment starting with the
  identifier's name (`revive` enforces it).
- **Do not over-comment internal logic.** Add an inline comment only when the
  *why* is non-obvious: a chosen algorithm, an invariant, an edge case.
- Never narrate the obvious.

### Naming

- Short, lower-case package name. No `util`/`common` grab-bags.
- Receivers short and consistent (`b *builder`, `q *Query`).
- Exported names carry their package: `filtrx.Where`, not `filtrx.WhereStruct`.

### Errors

- Wrap with `%w`. Listing helpers wrap causes in the sentinels `ErrCompile` and
  `ErrQuery` so callers can `errors.Is`.
- `Paginate` panics only on contradictory *programmer* arguments (First with
  Last, negatives) — never on runtime input. Input compilation returns an error.

### API design

- One obvious entry point per layer (`Where`, `Paginate`, `List`) plus a fluent
  `Query` builder. Keep the surface small.
- Zero values are meaningful: an empty filter compiles to no `WHERE`; zero
  `PagingParams` selects everything.

## Testing

- BDD `Convey` blocks ([goconvey](https://github.com/smartystreets/goconvey))
  with Given/When/Then nesting — this is the required style for all tests.
- Unit tests use `go-sqlmock` to assert exact generated SQL.
- The `integration` module runs one portable suite against SQLite (pure Go,
  always) and Postgres/MySQL (testcontainers, skipped without Docker).
- New behavior needs tests. Don't let coverage regress.

```go
func TestWhere(t *testing.T) {
    Convey("Given a tagged filter struct", t, func() {
        Convey("When a single holder field is set", func() {
            c, err := Where(userFilter{Status: Text{Eq: Some("active")}})
            Convey("Then one predicate is produced for its column", func() {
                So(err, ShouldBeNil)
                sql, _ := Build(c, Postgres)
                So(sql, ShouldEqual, `"status" = $1`)
            })
        })
    })
}
```

## Commits

- [Conventional Commits](https://www.conventionalcommits.org): `feat:`, `fix:`,
  `docs:`, `refactor:`, `test:`, `chore:`.
- Subject in imperative mood, ≤ ~72 chars. Body explains *why* when non-obvious.
