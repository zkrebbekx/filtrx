package filtrx

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	. "github.com/smartystreets/goconvey/convey"
)

func TestCursorRoundTrip(t *testing.T) {
	Convey("Given boundary values of assorted types", t, func() {
		now := time.Date(2026, 6, 15, 12, 0, 0, 123456789, time.UTC)
		in := []any{int64(42), "alice", true, 3.5, uint64(7), now, []byte("xy")}

		Convey("When encoded and decoded", func() {
			cur, err := encodeCursor(in)
			So(err, ShouldBeNil)
			out, derr := decodeCursor(cur)

			Convey("Then every value returns with its type preserved", func() {
				So(derr, ShouldBeNil)
				So(out[0], ShouldEqual, int64(42))
				So(out[1], ShouldEqual, "alice")
				So(out[2], ShouldEqual, true)
				So(out[3], ShouldEqual, 3.5)
				So(out[4], ShouldEqual, uint64(7))
				So(out[5].(time.Time).Equal(now), ShouldBeTrue)
				So(out[6], ShouldResemble, []byte("xy"))
			})
		})
	})

	Convey("Given a 64-bit id beyond float64's exact range (Snowflake-style)", t, func() {
		big := int64(1) << 62 // 4611686018427387904, not exactly representable as float64
		Convey("When encoded and decoded", func() {
			cur, err := encodeCursor([]any{big})
			So(err, ShouldBeNil)
			out, derr := decodeCursor(cur)
			Convey("Then the value survives exactly, with no precision loss", func() {
				So(derr, ShouldBeNil)
				So(out[0], ShouldEqual, big)
			})
		})
	})

	Convey("Given a NULL boundary value (a nullable keyset column)", t, func() {
		Convey("When encoded and decoded", func() {
			cur, err := encodeCursor([]any{nil, int64(7)})
			So(err, ShouldBeNil)
			out, derr := decodeCursor(cur)
			Convey("Then the NULL round-trips as a nil value", func() {
				So(derr, ShouldBeNil)
				So(out[0], ShouldBeNil)
				So(out[1], ShouldEqual, int64(7))
			})
		})
	})

	Convey("Given a malformed cursor string", t, func() {
		Convey("When decoded", func() {
			_, err := decodeCursor("not-base64-!!")
			Convey("Then it returns an invalid-cursor error", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "invalid cursor")
			})
		})
	})
}

func TestKeysetCond(t *testing.T) {
	Convey("Given a two-column order created_at ASC, id ASC", t, func() {
		order := []orderTerm{{col: "created_at"}, {col: "id"}}
		vals := []any{"2026-01-01", 5}

		Convey("When a forward keyset predicate is built", func() {
			sql, args := Build(keysetCond(order, vals, false), Postgres)
			Convey("Then it is the lexicographic OR-of-AND-prefixes", func() {
				So(sql, ShouldEqual,
					`("created_at" > $1 OR ("created_at" = $2 AND "id" > $3))`)
				So(args, ShouldResemble, []any{"2026-01-01", "2026-01-01", 5})
			})
		})

		Convey("When a backward keyset predicate is built", func() {
			sql, _ := Build(keysetCond(order, vals, true), Postgres)
			Convey("Then every comparison flips to less-than", func() {
				So(sql, ShouldEqual,
					`("created_at" < $1 OR ("created_at" = $2 AND "id" < $3))`)
			})
		})
	})

	Convey("Given a mixed order name ASC, id DESC", t, func() {
		order := []orderTerm{{col: "name"}, {col: "id", desc: true}}
		Convey("When a forward predicate is built", func() {
			sql, _ := Build(keysetCond(order, []any{"a", 5}, false), Postgres)
			Convey("Then the DESC column compares with less-than", func() {
				So(sql, ShouldEqual, `("name" > $1 OR ("name" = $2 AND "id" < $3))`)
			})
		})
	})
}

func TestListKeyset(t *testing.T) {
	Convey("Given a first keyset page ordered by id", t, func() {
		db, mock := newMock(t)
		defer func() { _ = db.Close() }()

		mock.ExpectQuery(`SELECT * FROM "users" ORDER BY "id" LIMIT $1`).
			WithArgs(3).
			WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).
				AddRow(1, "a").AddRow(2, "b").AddRow(3, "c"))

		q := From("users").OrderBy("id").Seek(SeekParams{Size: 2})
		var got []user
		info, err := List(context.Background(), db, q, &got)

		Convey("When listed", func() {
			Convey("Then it returns the page, flags more, and emits an end cursor", func() {
				So(err, ShouldBeNil)
				So(len(got), ShouldEqual, 2)
				So(info.Truncated, ShouldBeTrue)
				So(info.EndCursor, ShouldNotBeEmpty)
				vals, _ := decodeCursor(info.EndCursor)
				So(vals[0], ShouldEqual, int64(2))
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})
	})

	Convey("Given a forward page seeking after a cursor", t, func() {
		db, mock := newMock(t)
		defer func() { _ = db.Close() }()

		cur, _ := encodeCursor([]any{int64(2)})
		mock.ExpectQuery(`SELECT * FROM "users" WHERE "id" > $1 ORDER BY "id" LIMIT $2`).
			WithArgs(int64(2), 3).
			WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(3, "c"))

		q := From("users").OrderBy("id").Seek(SeekParams{After: cur, Size: 2})
		var got []user
		info, err := List(context.Background(), db, q, &got)

		Convey("When listed", func() {
			Convey("Then it seeks past the cursor and is not truncated", func() {
				So(err, ShouldBeNil)
				So(len(got), ShouldEqual, 1)
				So(got[0].ID, ShouldEqual, 3)
				So(info.Truncated, ShouldBeFalse)
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})
	})

	Convey("Given a backward page seeking before a cursor", t, func() {
		db, mock := newMock(t)
		defer func() { _ = db.Close() }()

		cur, _ := encodeCursor([]any{int64(5)})
		// Backward fetches in reversed (DESC) order, nearest the cursor first.
		mock.ExpectQuery(`SELECT * FROM "users" WHERE "id" < $1 ORDER BY "id" DESC LIMIT $2`).
			WithArgs(int64(5), 3).
			WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).
				AddRow(4, "d").AddRow(3, "c").AddRow(2, "b"))

		q := From("users").OrderBy("id").Seek(SeekParams{Before: cur, Size: 2})
		var got []user
		info, err := List(context.Background(), db, q, &got)

		Convey("When listed", func() {
			Convey("Then rows are restored to natural ascending order and trimmed", func() {
				So(err, ShouldBeNil)
				So(len(got), ShouldEqual, 2)
				So(got[0].ID, ShouldEqual, 3)
				So(got[1].ID, ShouldEqual, 4)
				So(info.Truncated, ShouldBeTrue)
				start, _ := decodeCursor(info.StartCursor)
				end, _ := decodeCursor(info.EndCursor)
				So(start[0], ShouldEqual, int64(3))
				So(end[0], ShouldEqual, int64(4))
				So(mock.ExpectationsWereMet(), ShouldBeNil)
			})
		})
	})

	Convey("Given a keyset request with no OrderBy", t, func() {
		db, _ := newMock(t)
		defer func() { _ = db.Close() }()

		q := From("users").Seek(SeekParams{Size: 2})
		var got []user
		_, err := List(context.Background(), db, q, &got)

		Convey("When listed", func() {
			Convey("Then it is rejected as a compile error", func() {
				So(err, ShouldNotBeNil)
				So(errors.Is(err, ErrCompile), ShouldBeTrue)
			})
		})
	})

	Convey("Given a keyset request whose order column has no struct field", t, func() {
		db, _ := newMock(t)
		defer func() { _ = db.Close() }()

		q := From("users").OrderBy("nonesuch").Seek(SeekParams{Size: 2})
		var got []user
		_, err := List(context.Background(), db, q, &got)

		Convey("When listed", func() {
			Convey("Then it fails before reaching SQL", func() {
				So(err, ShouldNotBeNil)
				So(errors.Is(err, ErrCompile), ShouldBeTrue)
			})
		})
	})
}
