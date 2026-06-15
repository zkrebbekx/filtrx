package filtrx

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	. "github.com/smartystreets/goconvey/convey"
)

func TestListConnection(t *testing.T) {
	Convey("Given a first keyset page assembled as a Relay connection", t, func() {
		db, mock := newMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectQuery(`SELECT * FROM "users" ORDER BY "id" LIMIT $1`).
			WithArgs(3).
			WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).
				AddRow(1, "a").AddRow(2, "b").AddRow(3, "c"))

		q := From("users").OrderBy("id").Seek(SeekParams{Size: 2})
		conn, err := ListConnection[user](context.Background(), db, q)

		Convey("When listed", func() {
			Convey("Then each edge carries its node and cursor, with Relay page info", func() {
				So(err, ShouldBeNil)
				So(len(conn.Edges), ShouldEqual, 2)
				So(conn.Edges[0].Node, ShouldResemble, user{ID: 1, Name: "a"})
				So(conn.Edges[0].Cursor, ShouldNotBeEmpty)
				So(conn.PageInfo.HasNextPage, ShouldBeTrue)
				So(conn.PageInfo.HasPreviousPage, ShouldBeFalse)
				So(conn.PageInfo.StartCursor, ShouldEqual, conn.Edges[0].Cursor)
				So(conn.PageInfo.EndCursor, ShouldEqual, conn.Edges[1].Cursor)
				So(conn.TotalCount, ShouldEqual, -1) // not requested
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})
	})

	Convey("Given a forward connection page after a cursor with a total", t, func() {
		db, mock := newMock(t)
		defer func() { _ = db.Close() }()

		cur, _ := encodeCursor([]any{int64(2)})
		mock.ExpectQuery(`SELECT * FROM "users" WHERE "id" > $1 ORDER BY "id" LIMIT $2`).
			WithArgs(int64(2), 3).
			WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(3, "c"))
		mock.ExpectQuery(`SELECT COUNT(*) FROM "users"`).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))

		q := From("users").OrderBy("id").Seek(SeekParams{After: cur, Size: 2, IncludeTotal: true})
		conn, err := ListConnection[user](context.Background(), db, q)

		Convey("When listed", func() {
			Convey("Then hasPreviousPage is set (came from a cursor) and total is filled", func() {
				So(err, ShouldBeNil)
				So(len(conn.Edges), ShouldEqual, 1)
				So(conn.PageInfo.HasNextPage, ShouldBeFalse)
				So(conn.PageInfo.HasPreviousPage, ShouldBeTrue)
				So(conn.TotalCount, ShouldEqual, 3)
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})
	})

	Convey("Given a query that is not in Seek mode", t, func() {
		db, _ := newMock(t)
		defer func() { _ = db.Close() }()

		q := From("users").OrderBy("id").Page(PagingParams{First: intp(2)})
		_, err := ListConnection[user](context.Background(), db, q)

		Convey("When asked for a connection", func() {
			Convey("Then it is a compile error", func() {
				So(err, ShouldNotBeNil)
				So(errors.Is(err, ErrCompile), ShouldBeTrue)
			})
		})
	})
}
