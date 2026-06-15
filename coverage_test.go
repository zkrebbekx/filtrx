package filtrx

import (
	"database/sql"
	"encoding/base64"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestCursorValueTypes(t *testing.T) {
	Convey("Given keys arriving as pointers, sql.Null* and Opt values", t, func() {
		n := 9
		in := []any{
			&n,                                   // non-nil pointer → deref
			(*int)(nil),                          // nil pointer → null
			sql.NullInt64{Int64: 7, Valid: true}, // Valuer → 7
			sql.NullString{Valid: false},         // Valuer NULL → null
			Some(3),                              // Opt is a Valuer → 3
		}
		cur, err := encodeCursor(in)
		So(err, ShouldBeNil)
		out, derr := decodeCursor(cur)

		Convey("When round-tripped", func() {
			Convey("Then each unwraps to its underlying value or nil", func() {
				So(derr, ShouldBeNil)
				So(out[0], ShouldEqual, int64(9))
				So(out[1], ShouldBeNil)
				So(out[2], ShouldEqual, int64(7))
				So(out[3], ShouldBeNil)
				So(out[4], ShouldEqual, int64(3))
			})
		})
	})

	Convey("Given an unsupported key type", t, func() {
		_, err := encodeCursor([]any{struct{ X int }{}})
		Convey("Then encoding fails", func() {
			So(err, ShouldNotBeNil)
		})
	})

	Convey("Given a cursor whose value contradicts its type tag", t, func() {
		raw := base64.RawURLEncoding.EncodeToString([]byte(`[{"t":"i","v":"notanumber"}]`))
		_, err := decodeCursor(Cursor(raw))
		Convey("Then decoding rejects it", func() {
			So(err, ShouldNotBeNil)
		})
	})

	Convey("Given a cursor with an unknown type tag", t, func() {
		raw := base64.RawURLEncoding.EncodeToString([]byte(`[{"t":"q","v":1}]`))
		_, err := decodeCursor(Cursor(raw))
		Convey("Then decoding rejects it", func() {
			So(err, ShouldNotBeNil)
		})
	})
}

func TestSeekConflict(t *testing.T) {
	Convey("Given a Seek with both After and Before", t, func() {
		q := From("t").OrderBy("id").Seek(SeekParams{After: "a", Before: "b", Size: 1})
		Convey("Then a compile error is deferred onto the query", func() {
			So(q.err, ShouldNotBeNil)
		})
	})
}

func TestBuildersAndOpt(t *testing.T) {
	Convey("Given the comparison builders that other tests don't exercise", t, func() {
		c := And(Ne("a", 1), Lte("b", 2), Like("c", "x%"), Gte("d", 3), IsNotNull("e"), NotIn("f", []int{1, 2}))
		sql, _ := Build(c, Postgres)
		Convey("Then each renders its operator", func() {
			So(sql, ShouldEqual, `("a" <> $1 AND "b" <= $2 AND "c" LIKE $3 AND "d" >= $4 AND "e" IS NOT NULL AND "f" NOT IN ($5, $6))`)
		})
	})

	Convey("Given Opt helpers", t, func() {
		Convey("Then None is unset and Or falls back; a set Opt reports its value", func() {
			So(None[int]().IsSet(), ShouldBeFalse)
			So(None[int]().Or(5), ShouldEqual, 5)
			So(Some(7).Or(5), ShouldEqual, 7)

			var o Opt[int]
			So(o.Scan(42), ShouldBeNil)
			v, ok := o.Get()
			So(ok, ShouldBeTrue)
			So(v, ShouldEqual, 42)
			So(o.Scan(nil), ShouldBeNil)
			So(o.IsSet(), ShouldBeFalse)

			dv, err := Some(3).Value()
			So(err, ShouldBeNil)
			So(dv, ShouldEqual, 3)
			ndv, _ := None[int]().Value()
			So(ndv, ShouldBeNil)
		})
	})
}

func TestDialectsMySQLSQLite(t *testing.T) {
	Convey("Given the same condition rendered for MySQL and SQLite", t, func() {
		c := And(Eq("a", 1), In("b", []int{2, 3}))

		Convey("When built for MySQL", func() {
			sql, _ := Build(c, MySQL)
			So(sql, ShouldEqual, "(`a` = ? AND `b` IN (?, ?))")
			So(MySQL.allRowsLimit(), ShouldEqual, "18446744073709551615")
			So(MySQL.supportsNullsOrdering(), ShouldBeFalse)
			So(MySQL.supportsWindowCount(), ShouldBeTrue)
		})

		Convey("When built for SQLite", func() {
			sql, _ := Build(c, SQLite)
			So(sql, ShouldEqual, `("a" = ? AND "b" IN (?, ?))`)
			So(SQLite.allRowsLimit(), ShouldEqual, "-1")
			So(SQLite.supportsNullsOrdering(), ShouldBeTrue)
			So(SQLite.supportsWindowCount(), ShouldBeTrue)
		})
	})
}
