package filtrx

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	. "github.com/smartystreets/goconvey/convey"
)

type custTotal struct {
	CustomerID int `db:"customer_id"`
	Total      int `db:"total"`
}

func TestGroupByHaving(t *testing.T) {
	Convey("Given a grouped, filtered, paged query with an aggregate HAVING", t, func() {
		db, mock := newMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectQuery(
			`SELECT customer_id, SUM(total) AS total, COUNT(*) OVER() AS _filtrx_total FROM "orders" WHERE "status" = $1 GROUP BY "customer_id" HAVING SUM(total) > $2 ORDER BY "customer_id" LIMIT $3 OFFSET $4`).
			WithArgs("paid", 100, 11, 0).
			WillReturnRows(sqlmock.NewRows([]string{"customer_id", "total", "_filtrx_total"}).
				AddRow(1, 500, 2).AddRow(2, 300, 2))

		q := From("orders").
			Select("customer_id", "SUM(total) AS total").
			Where(struct {
				Status Text `col:"status"`
			}{Status: Text{Eq: Some("paid")}}).
			GroupBy("customer_id").
			Having(Raw("SUM(total) > ?", 100)).
			OrderBy("customer_id").
			Page(PagingParams{First: intp(10), IncludeTotal: true})

		var got []custTotal
		info, err := List(context.Background(), db, q, &got)

		Convey("When listed", func() {
			Convey("Then WHERE, HAVING and LIMIT placeholders number in sequence", func() {
				So(err, ShouldBeNil)
				So(len(got), ShouldEqual, 2)
				So(got[0], ShouldResemble, custTotal{CustomerID: 1, Total: 500})
				So(info.Total, ShouldEqual, 2)
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})
	})

	Convey("Given a standalone count over a grouped query", t, func() {
		db, mock := newMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectQuery(
			`SELECT COUNT(*) FROM (SELECT 1 FROM "orders" GROUP BY "customer_id" HAVING SUM(total) > $1) AS _filtrx_grp`).
			WithArgs(100).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(4))

		q := From("orders").GroupBy("customer_id").Having(Raw("SUM(total) > ?", 100))

		Convey("When counted", func() {
			n, err := q.Count(context.Background(), db)
			Convey("Then the count wraps the grouped result as a subquery", func() {
				So(err, ShouldBeNil)
				So(n, ShouldEqual, 4)
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})
	})
}
