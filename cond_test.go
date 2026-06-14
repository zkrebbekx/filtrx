package filtrx

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestBuild(t *testing.T) {
	Convey("Given a condition tree", t, func() {

		Convey("When it is nil", func() {
			sql, args := Build(nil, Postgres)
			Convey("Then the SQL and args are empty", func() {
				So(sql, ShouldEqual, "")
				So(args, ShouldBeEmpty)
			})
		})

		Convey("When it is a single equality", func() {
			sql, args := Build(Eq("status", "active"), Postgres)
			Convey("Then it renders one placeholder and one argument", func() {
				So(sql, ShouldEqual, `"status" = $1`)
				So(args, ShouldResemble, []any{"active"})
			})
		})

		Convey("When nested groups mix AND and OR", func() {
			c := And(
				Eq("status", "active"),
				Or(
					Gt("age", 18),
					And(Eq("country", "US"), Gte("score", 100)),
				),
				In("role", []string{"admin", "mod"}),
			)
			sql, args := Build(c, Postgres)
			Convey("Then bracketing and placeholder order are exact", func() {
				So(sql, ShouldEqual,
					`("status" = $1 AND ("age" > $2 OR ("country" = $3 AND "score" >= $4)) AND "role" IN ($5, $6))`)
				So(args, ShouldResemble, []any{"active", 18, "US", 100, "admin", "mod"})
			})
		})

		Convey("When a group has a single effective child", func() {
			sql, _ := Build(And(Eq("a", 1), nil), Postgres)
			Convey("Then redundant parentheses are omitted", func() {
				So(sql, ShouldEqual, `"a" = $1`)
			})
		})

		Convey("When using NULL checks", func() {
			sql, args := Build(And(IsNull("deleted_at"), IsNotNull("email")), Postgres)
			Convey("Then no arguments are bound", func() {
				So(sql, ShouldEqual, `("deleted_at" IS NULL AND "email" IS NOT NULL)`)
				So(args, ShouldBeEmpty)
			})
		})

		Convey("When a raw fragment is spliced in", func() {
			c := And(Eq("a", 1), Raw("b->>'k' = ?", "v"))
			sql, args := Build(c, Postgres)
			Convey("Then its placeholders continue the surrounding numbering", func() {
				So(sql, ShouldEqual, `("a" = $1 AND b->>'k' = $2)`)
				So(args, ShouldResemble, []any{1, "v"})
			})
		})
	})

	Convey("Given the same tree across dialects", t, func() {
		c := And(Eq("a", 1), Eq("b", 2))
		Convey("When rendered for MySQL", func() {
			sql, _ := Build(c, MySQL)
			Convey("Then placeholders are ? and identifiers backticked", func() {
				So(sql, ShouldEqual, "(`a` = ? AND `b` = ?)")
			})
		})
		Convey("When rendered for Postgres", func() {
			sql, _ := Build(c, Postgres)
			Convey("Then placeholders are numbered and identifiers double-quoted", func() {
				So(sql, ShouldEqual, `("a" = $1 AND "b" = $2)`)
			})
		})
	})
}
