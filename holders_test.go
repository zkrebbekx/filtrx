package filtrx

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestHolders(t *testing.T) {
	Convey("Given a Range holder", t, func() {
		Convey("When several operators are set", func() {
			conds := Range[int]{Gte: Some(1), Lte: Some(9), Ne: Some(5)}.Predicates("n")
			sql, args := Build(And(conds...), Postgres)
			Convey("Then it emits one predicate per set operator", func() {
				So(sql, ShouldEqual, `("n" <> $1 AND "n" >= $2 AND "n" <= $3)`)
				So(args, ShouldResemble, []any{5, 1, 9})
			})
		})
		Convey("When it is the zero value", func() {
			conds := Range[int]{}.Predicates("n")
			Convey("Then it contributes nothing", func() {
				So(conds, ShouldBeEmpty)
			})
		})
		Convey("When IsNull is set true", func() {
			conds := Range[int]{IsNull: Some(true)}.Predicates("n")
			sql, args := Build(And(conds...), Postgres)
			Convey("Then it emits IS NULL with no argument", func() {
				So(sql, ShouldEqual, `"n" IS NULL`)
				So(args, ShouldBeEmpty)
			})
		})
	})

	Convey("Given a Match holder", t, func() {
		Convey("When equality and membership are set", func() {
			conds := Match[string]{Eq: Some("a"), In: []string{"b", "c"}}.Predicates("k")
			sql, args := Build(And(conds...), Postgres)
			Convey("Then both conditions are produced", func() {
				So(sql, ShouldEqual, `("k" = $1 AND "k" IN ($2, $3))`)
				So(args, ShouldResemble, []any{"a", "b", "c"})
			})
		})
	})

	Convey("Given a Text holder", t, func() {
		Convey("When Like and ILike are set", func() {
			conds := Text{Like: Some("A%"), ILike: Some("b%")}.Predicates("name")
			sql, _ := Build(And(conds...), Postgres)
			Convey("Then both pattern operators render", func() {
				So(sql, ShouldEqual, `("name" LIKE $1 AND "name" ILIKE $2)`)
			})
		})
	})
}
