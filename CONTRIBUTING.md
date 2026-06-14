# Contributing to filtrx

Thanks for your interest in improving filtrx.

## Development

```bash
make test          # unit tests with the race detector
make lint          # golangci-lint
make integration   # real-database suite (needs Docker)
```

Run `make lint test` before opening a pull request. The published library must
depend only on sqlx — keep heavyweight test dependencies (testcontainers, DB
drivers) inside the nested `integration` module.

## Design principles

- **The predicate tree is the safe core.** Anything that accepts user input
  compiles down to `Cond`; nothing concatenates SQL strings. New input surfaces
  must produce `Cond`, never raw SQL.
- **Whitelist by type.** A filter struct's types and tags fix the legal columns
  and operators. Request data may only supply values.
- **Small surface, meaningful zero values.** One entry point per layer; an empty
  filter is no `WHERE`, zero paging is "everything".

## Style

Follow the Go standard library style. Document every exported identifier with a
doc comment that starts with its name (`revive` enforces it). Keep internal
comments for the non-obvious *why*, not narration. See `CLAUDE.md` for the full
conventions.

## Tests

All tests use [goconvey](https://github.com/smartystreets/goconvey) with
Given/When/Then `Convey` nesting. Unit tests assert exact generated SQL with
`go-sqlmock`; behaviour that touches a real database goes in the `integration`
suite, which runs unchanged against SQLite, Postgres and MySQL. New behaviour
needs tests, and coverage should not regress.

## Commits

[Conventional Commits](https://www.conventionalcommits.org): `feat:`, `fix:`,
`docs:`, `refactor:`, `test:`, `chore:`. Imperative subject ≤ ~72 chars; explain
the *why* in the body when it isn't obvious.
