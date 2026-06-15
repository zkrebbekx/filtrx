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

	// A second table to exercise joins: categories carry a tier.
	db.MustExecContext(ctx, `DROP TABLE IF EXISTS categories`)
	db.MustExecContext(ctx, `CREATE TABLE categories (
		name VARCHAR(255) PRIMARY KEY,
		tier VARCHAR(255) NOT NULL
	)`)
	for name, tier := range map[string]string{"tools": "pro", "food": "basic"} {
		db.MustExecContext(ctx, db.Rebind(
			`INSERT INTO categories (name, tier) VALUES (?, ?)`), name, tier)
	}

	// A one-to-many table to exercise Exists: a product has many reviews.
	db.MustExecContext(ctx, `DROP TABLE IF EXISTS reviews`)
	db.MustExecContext(ctx, `CREATE TABLE reviews (
		id         INTEGER PRIMARY KEY,
		product_id INTEGER NOT NULL,
		rating     INTEGER NOT NULL
	)`)
	reviews := []struct{ id, productID, rating int }{
		{1, 1, 5}, {2, 1, 3}, // Hammer has two reviews, one five-star
		{3, 2, 4}, // Wrench has a four-star
		{4, 4, 5}, // Apple has a five-star
	}
	for _, r := range reviews {
		db.MustExecContext(ctx, db.Rebind(
			`INSERT INTO reviews (id, product_id, rating) VALUES (?, ?, ?)`),
			r.id, r.productID, r.rating)
	}
}

// reviewSub filters the child reviews table inside an Exists subquery.
type reviewSub struct {
	Rating filtrx.Range[int] `col:"r.rating"`
}

// productWithReview filters products by their one-to-many reviews, without
// fanning the result out, via a correlated EXISTS.
type productWithReview struct {
	Base    filtrx.Table             `table:"products" as:"p"`
	Reviews filtrx.Exists[reviewSub] `exists:"reviews r" on:"r.product_id = p.id"`
}

// reviewCount is the scan target for a grouped aggregate over reviews.
type reviewCount struct {
	ProductID int `db:"product_id"`
	N         int `db:"n"`
}

// productByTier joins products to categories and filters on the category tier,
// declaring the source entirely through marker fields.
type productByTier struct {
	Base filtrx.Table `table:"products" as:"p"`
	Cat  filtrx.Join  `table:"categories" as:"c" on:"c.name = p.category"`
	Tier filtrx.Text  `col:"c.tier"`
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

		Convey("When filtering through a joined table", func() {
			var got []product
			info, err := filtrx.List(ctx, db,
				filtrx.For(productByTier{Tier: filtrx.Text{Eq: filtrx.Some("pro")}}).
					On(d).
					OrderBy("p.id").
					Page(filtrx.PagingParams{First: ptr(10), IncludeTotal: true}),
				&got)
			Convey("Then only products in a pro-tier category come back", func() {
				So(err, ShouldBeNil)
				So(ids(got), ShouldResemble, []int{1, 2, 3})
				So(info.Total, ShouldEqual, 3)
			})
		})

		Convey("When filtering a one-to-many relation with Exists", func() {
			var got []product
			info, err := filtrx.List(ctx, db,
				filtrx.For(productWithReview{
					Reviews: filtrx.Exists[reviewSub]{
						When: filtrx.Some(true),
						Sub:  reviewSub{Rating: filtrx.Range[int]{Gte: filtrx.Some(5)}},
					},
				}).On(d).OrderBy("p.id").
					Page(filtrx.PagingParams{First: ptr(10), IncludeTotal: true}),
				&got)
			Convey("Then each matching base row appears once, with no fan-out", func() {
				So(err, ShouldBeNil)
				// Hammer (id 1) has two reviews but must appear once; Apple (id 4)
				// also has a five-star. The total counts entities, not joined rows.
				So(ids(got), ShouldResemble, []int{1, 4})
				So(info.Total, ShouldEqual, 2)
			})
		})

		Convey("When NOT EXISTS excludes rows with a matching child", func() {
			var got []product
			_, err := filtrx.List(ctx, db,
				filtrx.For(productWithReview{
					Reviews: filtrx.Exists[reviewSub]{When: filtrx.Some(false)},
				}).On(d).OrderBy("p.id"),
				&got)
			Convey("Then only products with no review at all remain", func() {
				So(err, ShouldBeNil)
				So(ids(got), ShouldResemble, []int{3, 5, 6})
			})
		})

		Convey("When paging by keyset cursor forward then backward", func() {
			var page1 []product
			info1, err := filtrx.List(ctx, db,
				From(d, "products").OrderBy("id").
					Seek(filtrx.SeekParams{Size: 2}),
				&page1)
			So(err, ShouldBeNil)

			var page2 []product
			info2, err := filtrx.List(ctx, db,
				From(d, "products").OrderBy("id").
					Seek(filtrx.SeekParams{After: info1.EndCursor, Size: 2}),
				&page2)
			So(err, ShouldBeNil)

			var back []product
			_, err = filtrx.List(ctx, db,
				From(d, "products").OrderBy("id").
					Seek(filtrx.SeekParams{Before: info2.StartCursor, Size: 2}),
				&back)
			So(err, ShouldBeNil)

			Convey("Then pages seek by cursor and reverse back to the first page", func() {
				So(ids(page1), ShouldResemble, []int{1, 2})
				So(info1.Truncated, ShouldBeTrue)
				So(ids(page2), ShouldResemble, []int{3, 4})
				So(ids(back), ShouldResemble, []int{1, 2})
			})
		})

		Convey("When grouping with a HAVING over an aggregate", func() {
			var got []reviewCount
			_, err := filtrx.List(ctx, db,
				From(d, "reviews").
					Select("product_id", "COUNT(*) AS n").
					GroupBy("product_id").
					Having(filtrx.Raw("COUNT(*) >= ?", 2)).
					OrderBy("product_id"),
				&got)
			Convey("Then only groups passing the HAVING come back", func() {
				So(err, ShouldBeNil)
				// Only product 1 has two reviews; products 2 and 4 have one each.
				So(got, ShouldResemble, []reviewCount{{ProductID: 1, N: 2}})
			})
		})

		Convey("When paging products as a Relay connection", func() {
			conn, err := filtrx.ListConnection[product](ctx, db,
				From(d, "products").OrderBy("id").Seek(filtrx.SeekParams{Size: 2}))
			So(err, ShouldBeNil)

			conn2, err := filtrx.ListConnection[product](ctx, db,
				From(d, "products").OrderBy("id").
					Seek(filtrx.SeekParams{After: conn.PageInfo.EndCursor, Size: 2}))
			So(err, ShouldBeNil)

			Convey("Then edges carry cursors and page info chains forward", func() {
				So(len(conn.Edges), ShouldEqual, 2)
				So(conn.Edges[0].Node.ID, ShouldEqual, 1)
				So(conn.Edges[0].Cursor, ShouldNotBeEmpty)
				So(conn.PageInfo.HasNextPage, ShouldBeTrue)
				So(conn.PageInfo.HasPreviousPage, ShouldBeFalse)

				So(conn2.Edges[0].Node.ID, ShouldEqual, 3)
				So(conn2.PageInfo.HasPreviousPage, ShouldBeTrue)
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

// runMutations exercises Update and Delete against a real engine. It reseeds the
// schema first and runs as a single leaf so its writes do not leak into the
// read-only suite (whose leaves share one database).
func runMutations(t *testing.T, db *sqlx.DB, d filtrx.Dialect) {
	ctx := context.Background()
	setupSchema(ctx, t, db)

	Convey("Given the seeded products, when updating then deleting by filter", t, func() {
		updated, err := From(d, "products").
			Where(productFilter{Category: filtrx.Text{Eq: filtrx.Some("food")}}).
			Update(ctx, db, map[string]any{"price": 999})
		So(err, ShouldBeNil)
		So(updated, ShouldEqual, 3) // apple, bread, milk

		deleted, err := From(d, "products").
			Where(productFilter{InStock: filtrx.Some(0)}).
			Delete(ctx, db)
		So(err, ShouldBeNil)
		So(deleted, ShouldEqual, 2) // drill and milk were out of stock

		Convey("Then the surviving rows reflect both mutations", func() {
			var got []product
			_, err := filtrx.List(ctx, db, From(d, "products").OrderBy("id"), &got)
			So(err, ShouldBeNil)
			So(ids(got), ShouldResemble, []int{1, 2, 4, 5})
			for _, p := range got {
				if p.Category == "food" {
					So(p.Price, ShouldEqual, 999)
				}
			}
		})
	})

	Convey("Given a delete with no filter, it is refused", t, func() {
		_, err := From(d, "categories").Delete(ctx, db)
		So(err, ShouldNotBeNil)
	})
}

func ptr(n int) *int { return &n }

// From builds a query already pinned to the engine's dialect.
func From(d filtrx.Dialect, table string) *filtrx.Query {
	return filtrx.From(table).On(d)
}
