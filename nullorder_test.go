package filtrx

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	. "github.com/smartystreets/goconvey/convey"
)

func TestOrderClause(t *testing.T) {
	Convey("Given a dialect with native NULLS ordering (Postgres)", t, func() {
		Convey("Then NULLS LAST and NULLS FIRST suffix the term", func() {
			So(orderClause(Postgres, `"x"`, false, nullsLast), ShouldEqual, `"x" NULLS LAST`)
			So(orderClause(Postgres, `"x"`, true, nullsFirst), ShouldEqual, `"x" DESC NULLS FIRST`)
			So(orderClause(Postgres, `"x"`, false, nullsDefault), ShouldEqual, `"x"`)
		})
	})

	Convey("Given MySQL, which lacks NULLS ordering", t, func() {
		Convey("Then it is emulated with a leading ISNULL() key", func() {
			So(orderClause(MySQL, "`x`", false, nullsLast), ShouldEqual, "ISNULL(`x`), `x`")
			So(orderClause(MySQL, "`x`", true, nullsFirst), ShouldEqual, "ISNULL(`x`) DESC, `x` DESC")
		})
	})
}

func TestKeysetCondNulls(t *testing.T) {
	Convey("Given a single nullable column ordered ASC NULLS LAST", t, func() {
		order := []orderTerm{{col: "x", nulls: nullsLast}}

		Convey("When the boundary is a non-NULL value, forward", func() {
			sql, _ := Build(keysetCond(order, []any{5}, false), Postgres)
			Convey("Then NULLs (which sort last) are also after it", func() {
				So(sql, ShouldEqual, `("x" > $1 OR "x" IS NULL)`)
			})
		})

		Convey("When the boundary is NULL, forward", func() {
			sql, _ := Build(keysetCond(order, []any{nil}, false), Postgres)
			Convey("Then nothing sorts after, so it matches nothing", func() {
				So(sql, ShouldEqual, `1=0`)
			})
		})
	})

	Convey("Given a single nullable column ordered ASC NULLS FIRST", t, func() {
		order := []orderTerm{{col: "x", nulls: nullsFirst}}

		Convey("When the boundary is NULL, forward", func() {
			sql, _ := Build(keysetCond(order, []any{nil}, false), Postgres)
			Convey("Then all non-NULL rows follow", func() {
				So(sql, ShouldEqual, `"x" IS NOT NULL`)
			})
		})

		Convey("When the boundary is non-NULL, forward", func() {
			sql, _ := Build(keysetCond(order, []any{5}, false), Postgres)
			Convey("Then NULLs (already before) are excluded", func() {
				So(sql, ShouldEqual, `"x" > $1`)
			})
		})
	})

	Convey("Given two columns: nullable ASC NULLS LAST, then a NOT NULL tiebreaker", t, func() {
		order := []orderTerm{{col: "a", nulls: nullsLast}, {col: "b"}}
		sql, _ := Build(keysetCond(order, []any{5, 10}, false), Postgres)
		Convey("When forward", func() {
			Convey("Then the prefix equality and tiebreaker compose correctly", func() {
				So(sql, ShouldEqual, `(("a" > $1 OR "a" IS NULL) OR ("a" = $2 AND "b" > $3))`)
			})
		})
	})
}

type scored struct {
	ID    int  `db:"id"`
	Score *int `db:"score"`
}

func TestListKeysetNullable(t *testing.T) {
	Convey("Given a keyset query over a nullable column with NULLS LAST", t, func() {
		db, mock := newMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectQuery(`SELECT * FROM "t" ORDER BY "score" NULLS LAST, "id" LIMIT $1`).
			WithArgs(3).
			WillReturnRows(sqlmock.NewRows([]string{"id", "score"}).
				AddRow(1, 10).AddRow(2, 20).AddRow(3, nil))

		q := From("t").OrderByNulls("score", false, false).OrderBy("id").
			Seek(SeekParams{Size: 2})
		var got []scored
		info, err := List(context.Background(), db, q, &got)

		Convey("When listed", func() {
			Convey("Then it pages and the end cursor carries the typed key values", func() {
				So(err, ShouldBeNil)
				So(len(got), ShouldEqual, 2)
				So(info.Truncated, ShouldBeTrue)
				vals, derr := decodeCursor(info.EndCursor)
				So(derr, ShouldBeNil)
				So(vals[0], ShouldEqual, int64(20)) // score of row 2
				So(vals[1], ShouldEqual, int64(2))  // id of row 2
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})
	})

	Convey("Given a cursor whose nullable key value is NULL", t, func() {
		db, mock := newMock(t)
		defer func() { _ = db.Close() }()

		// Boundary: score IS NULL, id = 3. NULLS LAST forward → only deeper
		// tiebreaker among the NULL-score rows applies.
		cur, _ := encodeCursor([]any{nil, int64(3)})
		mock.ExpectQuery(
			`SELECT * FROM "t" WHERE ("score" IS NULL AND "id" > $1) ORDER BY "score" NULLS LAST, "id" LIMIT $2`).
			WithArgs(int64(3), 3).
			WillReturnRows(sqlmock.NewRows([]string{"id", "score"}).AddRow(4, nil))

		q := From("t").OrderByNulls("score", false, false).OrderBy("id").
			Seek(SeekParams{After: cur, Size: 2})
		var got []scored
		_, err := List(context.Background(), db, q, &got)

		Convey("When listed", func() {
			Convey("Then the seek predicate handles the NULL boundary correctly", func() {
				So(err, ShouldBeNil)
				So(len(got), ShouldEqual, 1)
				So(got[0].ID, ShouldEqual, 4)
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})
	})
}
