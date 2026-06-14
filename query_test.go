package filtrx

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	. "github.com/smartystreets/goconvey/convey"
)

type user struct {
	ID   int    `db:"id"`
	Name string `db:"name"`
}

func newMock(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	return sqlx.NewDb(db, "postgres"), mock
}

func TestList(t *testing.T) {
	Convey("Given a forward page that requests the total", t, func() {
		db, mock := newMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectQuery(
			`SELECT *, COUNT(*) OVER() AS _filtrx_total FROM "users" WHERE "status" = $1 ORDER BY "id" LIMIT $2 OFFSET $3`).
			WithArgs("active", 11, 0).
			WillReturnRows(
				sqlmock.NewRows([]string{"id", "name", "_filtrx_total"}).
					AddRow(1, "ann", 42).
					AddRow(2, "bob", 42),
			)

		q := From("users").
			Where(struct {
				Status Text `col:"status"`
			}{Status: Text{Eq: Some("active")}}).
			OrderBy("id").
			Page(PagingParams{First: intp(10), IncludeTotal: true})

		var got []user
		info, err := List(context.Background(), db, q, &got)

		Convey("When listed", func() {
			Convey("Then rows scan and the window total is reported in one query", func() {
				So(err, ShouldBeNil)
				So(len(got), ShouldEqual, 2)
				So(got[0], ShouldResemble, user{ID: 1, Name: "ann"})
				So(info.Total, ShouldEqual, 42)
				So(info.Truncated, ShouldBeFalse)
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})
	})

	Convey("Given a page with more rows available than requested", t, func() {
		db, mock := newMock(t)
		defer func() { _ = db.Close() }()

		// First=2 fetches LIMIT 3 (limit+1); the third row signals more exist.
		mock.ExpectQuery(
			`SELECT *, COUNT(*) OVER() AS _filtrx_total FROM "users" ORDER BY "id" LIMIT $1 OFFSET $2`).
			WithArgs(3, 0).
			WillReturnRows(
				sqlmock.NewRows([]string{"id", "name", "_filtrx_total"}).
					AddRow(1, "a", 9).AddRow(2, "b", 9).AddRow(3, "c", 9),
			)

		q := From("users").OrderBy("id").Page(PagingParams{First: intp(2), IncludeTotal: true})
		var got []user
		info, err := List(context.Background(), db, q, &got)

		Convey("When listed", func() {
			Convey("Then the extra row is trimmed and Truncated is set", func() {
				So(err, ShouldBeNil)
				So(len(got), ShouldEqual, 2)
				So(info.Truncated, ShouldBeTrue)
				So(info.Total, ShouldEqual, 9)
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})
	})

	Convey("Given a Last page", t, func() {
		db, mock := newMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectQuery(`SELECT COUNT(*) FROM "users"`).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(100))
		mock.ExpectQuery(
			`SELECT * FROM "users" ORDER BY "id" LIMIT $1 OFFSET $2`).
			WithArgs(6, 95).
			WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(96, "z"))

		q := From("users").OrderBy("id").Page(PagingParams{Last: intp(5)})
		var got []user
		info, err := List(context.Background(), db, q, &got)

		Convey("When listed", func() {
			Convey("Then it pre-counts to resolve the end offset, then selects", func() {
				So(err, ShouldBeNil)
				So(info.Total, ShouldEqual, 100)
				So(info.Offset, ShouldEqual, 95)
				So(len(got), ShouldEqual, 1)
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})
	})

	Convey("Given an empty forward page that requests the total", t, func() {
		db, mock := newMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectQuery(
			`SELECT *, COUNT(*) OVER() AS _filtrx_total FROM "users" LIMIT $1 OFFSET $2`).
			WithArgs(11, 0).
			WillReturnRows(sqlmock.NewRows([]string{"id", "name", "_filtrx_total"}))
		// No window row came back, so the total falls back to a COUNT.
		mock.ExpectQuery(`SELECT COUNT(*) FROM "users"`).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

		q := From("users").Page(PagingParams{First: intp(10), IncludeTotal: true})
		var got []user
		info, err := List(context.Background(), db, q, &got)

		Convey("When listed", func() {
			Convey("Then it falls back to a COUNT for an accurate zero total", func() {
				So(err, ShouldBeNil)
				So(len(got), ShouldEqual, 0)
				So(info.Total, ShouldEqual, 0)
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})
	})

	Convey("Given SELECT * returning a column the struct omits", t, func() {
		db, mock := newMock(t)
		defer func() { _ = db.Close() }()

		// created_at has no field on user{ID,Name}; it must be ignored, not error.
		mock.ExpectQuery(`SELECT * FROM "users" LIMIT $1 OFFSET $2`).
			WithArgs(3, 0).
			WillReturnRows(
				sqlmock.NewRows([]string{"id", "name", "created_at"}).
					AddRow(1, "ann", "2026-01-01"),
			)

		q := From("users").Page(PagingParams{First: intp(2)})
		var got []user
		_, err := List(context.Background(), db, q, &got)

		Convey("When listed", func() {
			Convey("Then unmapped columns are discarded like an Unsafe sqlx scan", func() {
				So(err, ShouldBeNil)
				So(got, ShouldResemble, []user{{ID: 1, Name: "ann"}})
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})
	})

	Convey("Given a standalone count", t, func() {
		db, mock := newMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectQuery(`SELECT COUNT(*) FROM "users" WHERE "status" = $1`).
			WithArgs("active").
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(7))

		q := From("users").Where(struct {
			Status Text `col:"status"`
		}{Status: Text{Eq: Some("active")}})

		Convey("When counted", func() {
			n, err := q.Count(context.Background(), db)
			Convey("Then it returns the filtered total without fetching rows", func() {
				So(err, ShouldBeNil)
				So(n, ShouldEqual, 7)
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})
	})

	Convey("Given a whitelisted sort parsed from a request", t, func() {
		db, mock := newMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectQuery(`SELECT * FROM "users" ORDER BY "name", "created_at" DESC`).
			WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(1, "a"))

		q := From("users").Sort("name,-created", map[string]string{
			"name":    "name",
			"created": "created_at",
		})
		var got []user
		_, err := List(context.Background(), db, q, &got)

		Convey("When listed", func() {
			Convey("Then allowed keys map to columns with the right direction", func() {
				So(err, ShouldBeNil)
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})
	})

	Convey("Given a sort key that is not allowed", t, func() {
		db, _ := newMock(t)
		defer func() { _ = db.Close() }()

		q := From("users").Sort("password", map[string]string{"name": "name"})
		var got []user
		_, err := List(context.Background(), db, q, &got)

		Convey("When listed", func() {
			Convey("Then it is rejected as a compile error, never reaching SQL", func() {
				So(err, ShouldNotBeNil)
				So(errors.Is(err, ErrCompile), ShouldBeTrue)
			})
		})
	})

	Convey("Given a deferred compile error", t, func() {
		db, _ := newMock(t)
		defer func() { _ = db.Close() }()

		q := From("users").Where(struct {
			X Opt[int] `col:"x" op:"nope"`
		}{})
		var got []user
		_, err := List(context.Background(), db, q, &got)

		Convey("When listed", func() {
			Convey("Then List surfaces it as ErrCompile", func() {
				So(err, ShouldNotBeNil)
				So(errors.Is(err, ErrCompile), ShouldBeTrue)
			})
		})
	})
}
