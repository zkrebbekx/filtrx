# filtrx examples

A small HTTP server (`net/http`, Go 1.22 routing) over a real PostgreSQL database
that exercises **every filtrx feature** end to end: struct filters filled from the
query string, offset and keyset pagination, Relay connections, joins, correlated
`EXISTS`, grouping, full-text search with relevance, array operators, soft deletes
and filter-driven CRUD.

It's a separate Go module, so the published library stays dependency-light. The
domain is a tiny blog: **authors** (one) have many **articles**.

## Run it

With Docker (server + database):

```bash
docker compose up --build
# server on http://localhost:8080, schema applied and seeded on start
```

Or run the server locally against the compose database:

```bash
docker compose up -d db
go run .              # uses DATABASE_URL or the localhost default
```

`DATABASE_URL` defaults to `postgres://filtrx:filtrx@localhost:5432/filtrx?sslmode=disable`.

## A tour, by feature

```bash
B=http://localhost:8080

# Struct filter from the wire + whitelisted sort + offset page + one-query total
#   (store.ListArticles: Bind, BindPage, Sort, SoftDelete, List)
curl "$B/articles?status=published&views_gte=300&sort=-views&first=2&total=true"

# Nested filter operators: title pattern + range
curl "$B/articles?title_like=Go&views_gte=100"

# Keyset pagination as a Relay connection — page 1, then follow endCursor
#   (store.ArticleFeed: BindSeek, Seek, ListConnection)
curl "$B/articles/feed?size=3"
curl "$B/articles/feed?size=3&after=<endCursor-from-previous>"

# Full-text search ranked by relevance
#   (store.SearchArticles: FullText, OrderByRelevance)
curl "$B/articles/search?q=go+concurrency"

# Postgres array overlap on the tags text[]
#   (store.ArticlesByTag: Overlaps)
curl "$B/articles/by-tag?tag=go,history"

# Join filter across articles + authors
#   (store.ArticlesByAuthorName: For with Table/Join markers)
curl "$B/articles/by-author?name=Turing"

# One-to-many without fan-out: authors who have a published article
#   (store.PublishedAuthors: Exists)
curl "$B/authors/published"

# Grouping + HAVING: article counts per author
#   (store.ArticleCountsByAuthor: GroupBy, Having)
curl "$B/stats/articles-per-author?min=2"

# --- CRUD ---------------------------------------------------------------
# Create (plain sqlx insert)
curl -X POST "$B/articles" \
  -d '{"authorId":1,"title":"Keyset paging","body":"Seek beats offset.","status":"published","tags":["go","db"],"views":7}'

# Update one row (filtrx Update — same Cond machinery as the reads)
curl -X PATCH "$B/articles/8" -d '{"views":999,"status":"archived"}'

# Soft delete (Update of the scope column); the row then disappears from reads
curl -X DELETE "$B/articles/8"

# Hard delete (filtrx Delete)
curl -X DELETE "$B/articles/8?purge=true"
```

## Where each feature lives

| File         | What it shows                                                          |
| ------------ | --------------------------------------------------------------------- |
| `models.go`  | Domain scan types and the decorated filter structs (the contract).    |
| `store.go`   | One method per feature — read it as a filtrx cookbook.                 |
| `server.go`  | Thin HTTP handlers mapping requests to the store.                     |
| `schema.sql` | Tables (incl. a generated `tsvector` and a `text[]`), applied on start.|

Query-string conventions for `/articles` are filtrx's defaults: `status=active`
(equality), `views_gte=100&views_lt=500` (range), `id=1,2,3` (IN), `sort=-views`
(whitelisted), `first`/`last`/`after`/`before`/`total` (offset paging),
`size`/`after` (keyset).
