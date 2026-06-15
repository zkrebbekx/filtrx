package filtrx

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	. "github.com/smartystreets/goconvey/convey"
)

func TestSoftDelete(t *testing.T) {
	Convey("Given a soft-deleted query with an existing filter", t, func() {
		db, mock := newMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectQuery(
			`SELECT * FROM "users" WHERE ("status" = $1 AND "deleted_at" IS NULL) ORDER BY "id" LIMIT $2 OFFSET $3`).
			WithArgs("active", 11, 0).
			WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(1, "a"))

		q := From("users").
			Where(struct {
				Status Text `col:"status"`
			}{Status: Text{Eq: Some("active")}}).
			SoftDelete("deleted_at").
			OrderBy("id").
			Page(PagingParams{First: intp(10)})

		var got []user
		_, err := List(context.Background(), db, q, &got)

		Convey("When listed", func() {
			Convey("Then the live-rows scope ANDs onto the filter", func() {
				So(err, ShouldBeNil)
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})
	})

	Convey("Given a soft-deleted query with no other filter", t, func() {
		db, mock := newMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectQuery(`SELECT * FROM "users" WHERE "deleted_at" IS NULL`).
			WillReturnRows(sqlmock.NewRows([]string{"id", "name"}))

		q := From("users").SoftDelete("deleted_at")
		var got []user
		_, err := List(context.Background(), db, q, &got)

		Convey("When listed", func() {
			Convey("Then the scope alone is the WHERE", func() {
				So(err, ShouldBeNil)
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})
	})

	Convey("Given WithDeleted", t, func() {
		db, mock := newMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectQuery(`SELECT * FROM "users"`).
			WillReturnRows(sqlmock.NewRows([]string{"id", "name"}))

		q := From("users").SoftDelete("deleted_at").WithDeleted()
		var got []user
		_, err := List(context.Background(), db, q, &got)

		Convey("When listed", func() {
			Convey("Then no scope predicate is added", func() {
				So(err, ShouldBeNil)
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})
	})

	Convey("Given OnlyDeleted", t, func() {
		db, mock := newMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectQuery(`SELECT COUNT(*) FROM "users" WHERE "deleted_at" IS NOT NULL`).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(4))

		q := From("users").SoftDelete("deleted_at").OnlyDeleted()

		Convey("When counted", func() {
			n, err := q.Count(context.Background(), db)
			Convey("Then only soft-deleted rows are scoped in", func() {
				So(err, ShouldBeNil)
				So(n, ShouldEqual, 4)
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})
	})

	Convey("Given a soft-deleted delete", t, func() {
		db, mock := newMock(t)
		defer func() { _ = db.Close() }()

		// Even with no explicit filter, the scope makes it a filtered delete, so
		// Unfiltered is not needed and only live rows are touched.
		mock.ExpectExec(`DELETE FROM "users" WHERE "deleted_at" IS NULL`).
			WillReturnResult(sqlmock.NewResult(0, 2))

		q := From("users").SoftDelete("deleted_at")
		n, err := q.Delete(context.Background(), db)

		Convey("When deleted", func() {
			Convey("Then the scope filters it without Unfiltered", func() {
				So(err, ShouldBeNil)
				So(n, ShouldEqual, 2)
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})
	})
}

func TestRelevanceOrder(t *testing.T) {
	Convey("Given a relevance ordering on Postgres", t, func() {
		db, mock := newMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectQuery(
			`SELECT * FROM "docs" WHERE "body" @@ websearch_to_tsquery('english', $1) ORDER BY ts_rank("body", websearch_to_tsquery('english', $2)) DESC LIMIT $3 OFFSET $4`).
			WithArgs("fast car", "fast car", 11, 0).
			WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(1, "a"))

		q := From("docs").
			Cond(fullText{col: "body", config: "english", query: "fast car"}).
			OrderByRelevance("body", "fast car").
			Page(PagingParams{First: intp(10)})

		var got []user
		_, err := List(context.Background(), db, q, &got)

		Convey("When listed", func() {
			Convey("Then it ranks with ts_rank, binding the query again for the sort", func() {
				So(err, ShouldBeNil)
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})
	})

	Convey("Given a relevance ordering on MySQL", t, func() {
		q := From("docs").OrderByRelevance("body", "x").On(MySQL)
		sql, args := q.buildSelect("", nil, 0, 0, false)
		Convey("Then it uses the MATCH ... AGAINST score", func() {
			So(sql, ShouldContainSubstring, "ORDER BY MATCH(`body`) AGAINST(? IN NATURAL LANGUAGE MODE) DESC")
			So(args, ShouldResemble, []any{"x"})
		})
	})

	Convey("Given relevance ordering on SQLite", t, func() {
		db, _ := newMock(t)
		defer func() { _ = db.Close() }()

		q := From("docs").OrderByRelevance("body", "x").On(SQLite)
		var got []user
		_, err := List(context.Background(), db, q, &got)
		Convey("When listed", func() {
			Convey("Then it is rejected as unsupported", func() {
				So(err, ShouldNotBeNil)
				So(errors.Is(err, ErrCompile), ShouldBeTrue)
			})
		})
	})

	Convey("Given relevance ordering combined with Seek", t, func() {
		db, _ := newMock(t)
		defer func() { _ = db.Close() }()

		q := From("docs").OrderByRelevance("body", "x").Seek(SeekParams{Size: 5})
		var got []user
		_, err := List(context.Background(), db, q, &got)
		Convey("When listed", func() {
			Convey("Then keyset rejects relevance ordering", func() {
				So(err, ShouldNotBeNil)
				So(errors.Is(err, ErrCompile), ShouldBeTrue)
			})
		})
	})
}
