package main

import (
	"context"
	"net/url"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/zkrebbekx/filtrx"
)

// Store is the data-access layer. Every method shows a different filtrx feature.
type Store struct{ db *sqlx.DB }

// sortable whitelists the sort keys a request may use, mapping each to its real
// column so a sort parameter can never inject SQL.
var sortable = map[string]string{
	"title":   "title",
	"views":   "views",
	"created": "created_at",
}

// ListArticles is the bread-and-butter endpoint: a struct filter filled from the
// query string, whitelisted sorting, offset pagination with a one-query total,
// and a soft-delete scope that hides deleted rows. Demonstrates: Bind, BindPage,
// Sort, SoftDelete, List + PageInfo.
func (s *Store) ListArticles(ctx context.Context, vals url.Values) ([]Article, filtrx.PageInfo, error) {
	var f ArticleFilter
	if err := filtrx.Bind(vals, &f); err != nil {
		return nil, filtrx.PageInfo{}, err
	}
	page, err := filtrx.BindPage(vals)
	if err != nil {
		return nil, filtrx.PageInfo{}, err
	}

	q := filtrx.From("articles").
		Select(articleCols...).
		Where(f).
		SoftDelete("deleted_at").
		Sort(vals.Get("sort"), sortable).
		OrderBy("id"). // stable tiebreaker
		Page(page)

	out := []Article{}
	info, err := filtrx.List(ctx, s.db, q, &out)
	return out, info, err
}

// ArticleFeed is keyset (cursor) pagination assembled as a GraphQL Relay
// connection — flat cost at any depth. Demonstrates: BindSeek, Seek,
// ListConnection. Drive it with ?size=10 then ?after=<endCursor>&size=10.
func (s *Store) ArticleFeed(ctx context.Context, vals url.Values) (filtrx.Connection[Article], error) {
	seek, err := filtrx.BindSeek(vals)
	if err != nil {
		return filtrx.Connection[Article]{}, err
	}
	if seek.Size == 0 {
		seek.Size = 10
	}
	q := filtrx.From("articles").
		Select(articleCols...).
		SoftDelete("deleted_at").
		OrderByDesc("created_at").
		OrderBy("id"). // unique tiebreaker → total order
		Seek(seek)

	return filtrx.ListConnection[Article](ctx, s.db, q)
}

// SearchArticles runs a full-text query and orders by relevance. Demonstrates:
// FullText holder, OrderByRelevance.
func (s *Store) SearchArticles(ctx context.Context, query string) ([]Article, error) {
	f := ArticleSearch{Body: filtrx.FullText{Query: filtrx.Some(query)}}
	q := filtrx.From("articles").
		Select(articleCols...).
		Where(f).
		SoftDelete("deleted_at").
		OrderByRelevance("search_vec", query)

	out := []Article{}
	_, err := filtrx.List(ctx, s.db, q, &out)
	return out, err
}

// ArticlesByAuthorName filters across a join. Demonstrates: For (Table/Join
// markers), qualified columns.
func (s *Store) ArticlesByAuthorName(ctx context.Context, name string) ([]Article, error) {
	q := filtrx.For(ArticleByAuthor{
		Status: filtrx.Match[string]{Eq: filtrx.Some("published")},
		Name:   filtrx.Text{Like: filtrx.Some("%" + name + "%")},
	}).
		// Qualify the projection: in a join, a bare "id" is ambiguous across both
		// tables, so name the base table's columns explicitly.
		Select("a.id", "a.author_id", "a.title", "a.body", "a.status", "a.tags", "a.views", "a.created_at").
		OrderBy("a.id")

	out := []Article{}
	_, err := filtrx.List(ctx, s.db, q, &out)
	return out, err
}

// PublishedAuthors lists authors with at least one published article, without
// fan-out. Demonstrates: Exists (correlated EXISTS, one-to-many).
func (s *Store) PublishedAuthors(ctx context.Context) ([]Author, error) {
	q := filtrx.For(AuthorWithPublished{
		Articles: filtrx.Exists[articleSub]{
			When: filtrx.Some(true),
			Sub:  articleSub{Status: filtrx.Match[string]{Eq: filtrx.Some("published")}},
		},
	}).
		Select("au.id", "au.name", "au.email").
		OrderBy("au.id")

	out := []Author{}
	_, err := filtrx.List(ctx, s.db, q, &out)
	return out, err
}

// ArticleCountsByAuthor groups and filters groups. Demonstrates: GroupBy, Having,
// an aggregate projection.
func (s *Store) ArticleCountsByAuthor(ctx context.Context, min int) ([]AuthorArticleCount, error) {
	q := filtrx.From("articles").
		Select("author_id", "COUNT(*) AS n").
		SoftDelete("deleted_at").
		GroupBy("author_id").
		Having(filtrx.Raw("COUNT(*) >= ?", min)).
		OrderByDesc("n").
		OrderBy("author_id")

	out := []AuthorArticleCount{}
	_, err := filtrx.List(ctx, s.db, q, &out)
	return out, err
}

// ArticlesByTag matches the Postgres tags text[] with the array overlap operator.
// Demonstrates: the Overlaps builder via Cond.
func (s *Store) ArticlesByTag(ctx context.Context, tags []string) ([]Article, error) {
	q := filtrx.From("articles").
		Select(articleCols...).
		Cond(filtrx.Overlaps("tags", pq.Array(tags))).
		SoftDelete("deleted_at").
		OrderBy("id")

	out := []Article{}
	_, err := filtrx.List(ctx, s.db, q, &out)
	return out, err
}

// CreateArticle inserts a row. Writes that create rows are plain sqlx; filtrx
// drives the filtered reads and the bulk updates/deletes below.
func (s *Store) CreateArticle(ctx context.Context, a *Article) error {
	return s.db.QueryRowxContext(ctx,
		`INSERT INTO articles (author_id, title, body, status, tags, views)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id, created_at`,
		a.AuthorID, a.Title, a.Body, a.Status, pq.Array(a.Tags), a.Views,
	).Scan(&a.ID, &a.CreatedAt)
}

// UpdateArticle applies a partial update to one article. Demonstrates: Update
// (filter-driven write) — the same Cond machinery as the reads.
func (s *Store) UpdateArticle(ctx context.Context, id int, set map[string]any) (int64, error) {
	return filtrx.From("articles").
		Cond(filtrx.Eq("id", id)).
		SoftDelete("deleted_at"). // never update an already-deleted row
		Update(ctx, s.db, set)
}

// SoftDeleteArticle marks an article deleted. Demonstrates: a soft delete as an
// Update of the scope column.
func (s *Store) SoftDeleteArticle(ctx context.Context, id int) (int64, error) {
	return filtrx.From("articles").
		Cond(filtrx.Eq("id", id)).
		SoftDelete("deleted_at").
		Update(ctx, s.db, map[string]any{"deleted_at": time.Now()})
}

// PurgeArticle hard-deletes an article. Demonstrates: Delete, and that without a
// filter the call is refused unless Unfiltered is set.
func (s *Store) PurgeArticle(ctx context.Context, id int) (int64, error) {
	return filtrx.From("articles").
		Cond(filtrx.Eq("id", id)).
		Delete(ctx, s.db)
}
