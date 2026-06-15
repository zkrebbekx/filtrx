package main

import (
	"time"

	"github.com/lib/pq"
	"github.com/zkrebbekx/filtrx"
)

// --- Domain types (scan targets) -------------------------------------------

// Author is a writer. Authors have many articles (one-to-many).
type Author struct {
	ID    int    `db:"id" json:"id"`
	Name  string `db:"name" json:"name"`
	Email string `db:"email" json:"email"`
}

// Article is the central table: it belongs to an author (many-to-one), carries a
// Postgres text[] of tags and a generated tsvector for full-text search, and is
// soft-deleted via deleted_at.
type Article struct {
	ID        int            `db:"id" json:"id"`
	AuthorID  int            `db:"author_id" json:"authorId"`
	Title     string         `db:"title" json:"title"`
	Body      string         `db:"body" json:"body"`
	Status    string         `db:"status" json:"status"`
	Tags      pq.StringArray `db:"tags" json:"tags"`
	Views     int            `db:"views" json:"views"`
	CreatedAt time.Time      `db:"created_at" json:"createdAt"`
}

// articleCols is the explicit projection for Article: it omits the generated
// search_vec (a tsvector, not a scannable Go value) and deleted_at.
var articleCols = []string{"id", "author_id", "title", "body", "status", "tags", "views", "created_at"}

// AuthorArticleCount is the scan target for the grouped aggregate.
type AuthorArticleCount struct {
	AuthorID int `db:"author_id" json:"authorId"`
	Count    int `db:"n" json:"count"`
}

// --- Filter structs (the contract, filled from the wire) -------------------

// ArticleFilter is the simple single-table filter. It is decorated once and
// filled straight from the query string by filtrx.Bind: ?status=published,
// ?title_like=Go%25, ?views_gte=100, ?id=1,2,3, and nested OR groups from JSON.
type ArticleFilter struct {
	Status filtrx.Match[string] `col:"status"`
	Title  filtrx.Text          `col:"title"`
	Views  filtrx.Range[int]    `col:"views"`
	IDs    []int                `col:"id" op:"in"`
	Any    []ArticleFilter      `group:"or"`
}

// ArticleSearch is a full-text filter over the generated tsvector column. Query
// reads the bare/eq wire value, so ?q maps onto it after we copy it in.
type ArticleSearch struct {
	Body filtrx.FullText `col:"search_vec"`
}

// ArticleByAuthor is a join filter (many-to-one): articles joined to their
// author, filterable by columns on either side. filtrx.For reads the table and
// join from the marker fields.
type ArticleByAuthor struct {
	Base   filtrx.Table         `table:"articles" as:"a"`
	Author filtrx.Join          `table:"authors" as:"au" on:"au.id = a.author_id"`
	Status filtrx.Match[string] `col:"a.status"`
	Name   filtrx.Text          `col:"au.name"`
}

// AuthorWithPublished filters authors by a one-to-many relationship without
// fan-out, via a correlated EXISTS: authors who have at least one published
// article.
type AuthorWithPublished struct {
	Base     filtrx.Table              `table:"authors" as:"au"`
	Articles filtrx.Exists[articleSub] `exists:"articles a" on:"a.author_id = au.id"`
}

type articleSub struct {
	Status filtrx.Match[string] `col:"a.status"`
}
