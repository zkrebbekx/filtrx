package filtrx

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	. "github.com/smartystreets/goconvey/convey"
)

type orderJoinFilter struct {
	Base   Table      `table:"users" as:"u"`
	Orders Join       `table:"orders" as:"o" on:"o.user_id = u.id" type:"left"`
	Status Text       `col:"u.status"`
	Total  Range[int] `col:"o.total"`
}

func TestJoins(t *testing.T) {
	Convey("Given a join-backed filter struct", t, func() {

		Convey("When compiled with qualified columns", func() {
			c, err := Where(orderJoinFilter{
				Status: Text{Eq: Some("active")},
				Total:  Range[int]{Gt: Some(100)},
			})
			sql, args := Build(c, Postgres)
			Convey("Then dotted columns are quoted per segment", func() {
				So(err, ShouldBeNil)
				So(sql, ShouldEqual, `("u"."status" = $1 AND "o"."total" > $2)`)
				So(args, ShouldResemble, []any{"active", 100})
			})
		})

		Convey("When listed via For", func() {
			db, mock := newMock(t)
			defer func() { _ = db.Close() }()

			mock.ExpectQuery(
				`SELECT "u".* FROM "users" "u" LEFT JOIN "orders" "o" ON o.user_id = u.id WHERE ("u"."status" = $1 AND "o"."total" > $2) LIMIT $3 OFFSET $4`).
				WithArgs("active", 100, 3, 0).
				WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(1, "ann"))

			q := For(orderJoinFilter{
				Status: Text{Eq: Some("active")},
				Total:  Range[int]{Gt: Some(100)},
			}).Page(PagingParams{First: intp(2)})

			var got []user
			_, err := List(context.Background(), db, q, &got)

			Convey("Then the FROM, JOIN and base-table projection are generated", func() {
				So(err, ShouldBeNil)
				So(got, ShouldResemble, []user{{ID: 1, Name: "ann"}})
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})

		Convey("When counted via For", func() {
			db, mock := newMock(t)
			defer func() { _ = db.Close() }()

			mock.ExpectQuery(
				`SELECT COUNT(*) FROM "users" "u" LEFT JOIN "orders" "o" ON o.user_id = u.id WHERE "u"."status" = $1`).
				WithArgs("active").
				WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(4))

			q := For(orderJoinFilter{Status: Text{Eq: Some("active")}})
			n, err := q.Count(context.Background(), db)

			Convey("Then COUNT runs over the joined source", func() {
				So(err, ShouldBeNil)
				So(n, ShouldEqual, 4)
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})
	})

	Convey("Given invalid join declarations", t, func() {
		Convey("When For is given a struct without a Table field", func() {
			type noTable struct {
				Status Text `col:"status"`
			}
			q := For(noTable{})
			_, err := List(context.Background(), nil, q, &[]user{})
			Convey("Then it fails as a compile error", func() {
				So(err, ShouldNotBeNil)
				So(errors.Is(err, ErrCompile), ShouldBeTrue)
			})
		})

		Convey("When a Join field omits its on clause", func() {
			type badJoin struct {
				Base   Table `table:"users" as:"u"`
				Orders Join  `table:"orders" as:"o"`
			}
			_, err := Where(badJoin{})
			Convey("Then compilation fails", func() {
				So(err, ShouldNotBeNil)
			})
		})

		Convey("When Join fields appear without a Table field", func() {
			type orphan struct {
				Orders Join `table:"orders" as:"o" on:"o.user_id = id"`
			}
			_, err := Where(orphan{})
			Convey("Then compilation fails", func() {
				So(err, ShouldNotBeNil)
			})
		})
	})

	Convey("Given a join filter rendered for MySQL", t, func() {
		c, _ := Where(orderJoinFilter{Status: Text{Eq: Some("active")}})
		sql, _ := Build(c, MySQL)
		Convey("When built", func() {
			Convey("Then qualified identifiers use backticks per segment", func() {
				So(sql, ShouldEqual, "`u`.`status` = ?")
			})
		})
	})
}
