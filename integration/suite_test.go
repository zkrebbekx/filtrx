//go:build integration

// Package integration exercises filtrx against real database engines. The same
// behavioural suite runs unchanged against SQLite, PostgreSQL and MySQL, proving
// the generated SQL — placeholders, identifier quoting, COUNT(*) OVER() totals,
// LIMIT/OFFSET windows — is portable across all three.
//
// Run with: go test -tags=integration ./...
// SQLite always runs (pure Go). PostgreSQL and MySQL use testcontainers and are
// skipped automatically when Docker is unavailable.
package integration

import (
	"context"
	"testing"

	"github.com/jmoiron/sqlx"
	. "github.com/smartystreets/goconvey/convey"
	"github.com/zkrebbekx/filtrx"
)

// product is the scan target and doubles as documentation of the table shape.
type product struct {
	ID       int    `db:"id"`
	Name     string `db:"name"`
	Category string `db:"category"`
	Price    int    `db:"price"`
	InStock  int    `db:"in_stock"`
}

// productFilter is the request-facing filter, decorated once.
type productFilter struct {
	Category filtrx.Text       `col:"category"`
	Name     filtrx.Text       `col:"name"`
	Price    filtrx.Range[int] `col:"price"`
	InStock  filtrx.Opt[int]   `col:"in_stock" op:"eq"`
	IDs      []int             `col:"id" op:"in"`
	Any      []productFilter   `group:"or"`
}

var seed = []product{
	{1, "Hammer", "tools", 1000, 1},
	{2, "Wrench", "tools", 1500, 1},
	{3, "Drill", "tools", 5000, 0},
	{4, "Apple", "food", 200, 1},
	{5, "Bread", "food", 300, 1},
	{6, "Milk", "food", 150, 0},
}

func setupSchema(ctx context.Context, t *testing.T, db *sqlx.DB) {
	t.Helper()
	db.MustExecContext(ctx, `DROP TABLE IF EXISTS products`)
	db.MustExecContext(ctx, `CREATE TABLE products (
		id       INTEGER PRIMARY KEY,
		name     VARCHAR(255) NOT NULL,
		category VARCHAR(255) NOT NULL,
		price    INTEGER NOT NULL,
		in_stock INTEGER NOT NULL
	)`)
	for _, p := range seed {
		db.MustExecContext(ctx, db.Rebind(
			`INSERT INTO products (id, name, category, price, in_stock) VALUES (?, ?, ?, ?, ?)`),
			p.ID, p.Name, p.Category, p.Price, p.InStock)
	}
}

func ids(ps []product) []int {
	out := make([]int, len(ps))
	for i, p := range ps {
		out[i] = p.ID
	}
	return out
}

// runSuite is the portable behavioural contract executed against every engine.
func runSuite(t *testing.T, db *sqlx.DB, d filtrx.Dialect) {
	ctx := context.Background()
	setupSchema(ctx, t, db)

	Convey("Given a products table seeded across two categories", t, func() {

		Convey("When filtering by category equality", func() {
			var got []product
			_, err := filtrx.List(ctx, db,
				From(d, "products").
					Where(productFilter{Category: filtrx.Text{Eq: filtrx.Some("tools")}}).
					OrderBy("id"),
				&got)
			Convey("Then only that category is returned", func() {
				So(err, ShouldBeNil)
				So(ids(got), ShouldResemble, []int{1, 2, 3})
			})
		})

		Convey("When filtering by a price range", func() {
			var got []product
			_, err := filtrx.List(ctx, db,
				From(d, "products").
					Where(productFilter{Price: filtrx.Range[int]{Gte: filtrx.Some(1000), Lte: filtrx.Some(2000)}}).
					OrderBy("id"),
				&got)
			Convey("Then both bounds apply", func() {
				So(err, ShouldBeNil)
				So(ids(got), ShouldResemble, []int{1, 2})
			})
		})

		Convey("When filtering by IN membership", func() {
			var got []product
			_, err := filtrx.List(ctx, db,
				From(d, "products").
					Where(productFilter{IDs: []int{1, 4, 6}}).
					OrderBy("id"),
				&got)
			Convey("Then the listed ids are returned", func() {
				So(err, ShouldBeNil)
				So(ids(got), ShouldResemble, []int{1, 4, 6})
			})
		})

		Convey("When combining an OR group with an outer condition", func() {
			var got []product
			_, err := filtrx.List(ctx, db,
				From(d, "products").
					Where(productFilter{
						InStock: filtrx.Some(1),
						Any: []productFilter{
							{Category: filtrx.Text{Eq: filtrx.Some("food")}},
							{Price: filtrx.Range[int]{Gt: filtrx.Some(4000)}},
						},
					}).
					OrderBy("id"),
				&got)
			Convey("Then in-stock items that are food or expensive are returned", func() {
				So(err, ShouldBeNil)
				So(ids(got), ShouldResemble, []int{4, 5})
			})
		})

		Convey("When paging forward with a total", func() {
			var got []product
			info, err := filtrx.List(ctx, db,
				From(d, "products").OrderBy("id").
					Page(filtrx.PagingParams{First: ptr(2), IncludeTotal: true}),
				&got)
			Convey("Then the window, total and truncation flag are correct in one query", func() {
				So(err, ShouldBeNil)
				So(ids(got), ShouldResemble, []int{1, 2})
				So(info.Total, ShouldEqual, 6)
				So(info.Truncated, ShouldBeTrue)
			})
		})

		Convey("When paging from the end", func() {
			var got []product
			info, err := filtrx.List(ctx, db,
				From(d, "products").OrderBy("id").
					Page(filtrx.PagingParams{Last: ptr(2)}),
				&got)
			Convey("Then the last records are returned with the resolved offset", func() {
				So(err, ShouldBeNil)
				So(ids(got), ShouldResemble, []int{5, 6})
				So(info.Total, ShouldEqual, 6)
				So(info.Offset, ShouldEqual, 4)
			})
		})

		Convey("When paging after an offset", func() {
			var got []product
			_, err := filtrx.List(ctx, db,
				From(d, "products").OrderBy("id").
					Page(filtrx.PagingParams{After: ptr(4), First: ptr(10)}),
				&got)
			Convey("Then only records past the offset are returned", func() {
				So(err, ShouldBeNil)
				So(ids(got), ShouldResemble, []int{6})
			})
		})

		Convey("When a filtered page is empty but a total is requested", func() {
			var got []product
			info, err := filtrx.List(ctx, db,
				From(d, "products").
					Where(productFilter{Category: filtrx.Text{Eq: filtrx.Some("nonexistent")}}).
					OrderBy("id").
					Page(filtrx.PagingParams{First: ptr(5), IncludeTotal: true}),
				&got)
			Convey("Then it reports zero via the count fallback", func() {
				So(err, ShouldBeNil)
				So(got, ShouldBeEmpty)
				So(info.Total, ShouldEqual, 0)
			})
		})
	})
}

func ptr(n int) *int { return &n }

// From builds a query already pinned to the engine's dialect.
func From(d filtrx.Dialect, table string) *filtrx.Query {
	return filtrx.From(table).On(d)
}
