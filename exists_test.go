package filtrx

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

type orderSub struct {
	Status Text       `col:"o.status"`
	Total  Range[int] `col:"o.total"`
}

type userWithOrders struct {
	Base   Table            `table:"users" as:"u"`
	Status Text             `col:"u.status"`
	Orders Exists[orderSub] `exists:"orders o" on:"o.user_id = u.id"`
	NoPaid Exists[orderSub] `exists:"orders o" on:"o.user_id = u.id"`
}

func TestExists(t *testing.T) {
	Convey("Given an Exists field toggled on with a sub-filter", t, func() {
		f := userWithOrders{
			Orders: Exists[orderSub]{
				When: Some(true),
				Sub:  orderSub{Status: Text{Eq: Some("paid")}, Total: Range[int]{Gt: Some(100)}},
			},
		}
		c, _, err := compileFilter(f)

		Convey("When built", func() {
			sql, args := Build(c, Postgres)
			Convey("Then it renders a correlated EXISTS with the sub-predicates", func() {
				So(err, ShouldBeNil)
				So(sql, ShouldEqual,
					`EXISTS (SELECT 1 FROM orders o WHERE o.user_id = u.id AND ("o"."status" = $1 AND "o"."total" > $2))`)
				So(args, ShouldResemble, []any{"paid", 100})
			})
		})
	})

	Convey("Given an Exists field toggled off (When unset)", t, func() {
		f := userWithOrders{Status: Text{Eq: Some("active")}}
		c, _, err := compileFilter(f)

		Convey("When built", func() {
			sql, args := Build(c, Postgres)
			Convey("Then only the plain field contributes; no EXISTS appears", func() {
				So(err, ShouldBeNil)
				So(sql, ShouldEqual, `"u"."status" = $1`)
				So(args, ShouldResemble, []any{"active"})
			})
		})
	})

	Convey("Given an Exists field set to Some(false)", t, func() {
		f := userWithOrders{
			NoPaid: Exists[orderSub]{When: Some(false)},
		}
		c, _, err := compileFilter(f)

		Convey("When built", func() {
			sql, _ := Build(c, Postgres)
			Convey("Then it negates to NOT EXISTS with no extra predicate", func() {
				So(err, ShouldBeNil)
				So(sql, ShouldEqual, `NOT EXISTS (SELECT 1 FROM orders o WHERE o.user_id = u.id)`)
			})
		})
	})

	Convey("Given an Exists field with an empty sub-filter", t, func() {
		f := userWithOrders{Orders: Exists[orderSub]{When: Some(true)}}
		c, _, err := compileFilter(f)

		Convey("When built", func() {
			sql, _ := Build(c, Postgres)
			Convey("Then it tests for any matching child row", func() {
				So(err, ShouldBeNil)
				So(sql, ShouldEqual, `EXISTS (SELECT 1 FROM orders o WHERE o.user_id = u.id)`)
			})
		})
	})

	Convey("Given a filter side by side: a plain field and an EXISTS", t, func() {
		f := userWithOrders{
			Status: Text{Eq: Some("active")},
			Orders: Exists[orderSub]{When: Some(true), Sub: orderSub{Status: Text{Eq: Some("paid")}}},
		}
		c, _, err := compileFilter(f)

		Convey("When built", func() {
			sql, args := Build(c, Postgres)
			Convey("Then both are AND-joined with correct placeholder numbering", func() {
				So(err, ShouldBeNil)
				So(sql, ShouldEqual,
					`("u"."status" = $1 AND EXISTS (SELECT 1 FROM orders o WHERE o.user_id = u.id AND "o"."status" = $2))`)
				So(args, ShouldResemble, []any{"active", "paid"})
			})
		})
	})

	Convey("Given an Exists field missing its exists tag", t, func() {
		type badFilter struct {
			X Exists[orderSub] `on:"o.id = u.id"`
		}
		_, _, err := compileFilter(badFilter{})
		Convey("When compiled", func() {
			Convey("Then plan compilation fails for the missing source", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "needs an exists tag")
			})
		})
	})

	Convey("Given an Exists field missing its on tag", t, func() {
		type badFilter struct {
			X Exists[orderSub] `exists:"orders o"`
		}
		_, _, err := compileFilter(badFilter{})
		Convey("When compiled", func() {
			Convey("Then it fails for the missing correlation", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "needs an on tag")
			})
		})
	})
}
