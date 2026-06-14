package filtrx

import (
	"net/url"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

type bindFilter struct {
	Status Text       `col:"status"`
	Name   Text       `col:"name"`
	Age    Range[int] `col:"age"`
	Active Opt[bool]  `col:"active" op:"eq"`
	Roles  []string   `col:"role" op:"in" q:"role"`
}

func TestBind(t *testing.T) {
	Convey("Given a query string and a filter struct", t, func() {

		Convey("When a bare parameter maps to a holder's equality", func() {
			var f bindFilter
			err := Bind(url.Values{"status": {"active"}}, &f)
			c, _ := Where(f)
			sql, args := Build(c, Postgres)
			Convey("Then it becomes an equality predicate", func() {
				So(err, ShouldBeNil)
				So(sql, ShouldEqual, `"status" = $1`)
				So(args, ShouldResemble, []any{"active"})
			})
		})

		Convey("When suffixed parameters set range operators", func() {
			var f bindFilter
			err := Bind(url.Values{"age_gte": {"18"}, "age_lt": {"65"}}, &f)
			c, _ := Where(f)
			sql, args := Build(c, Postgres)
			Convey("Then both bounds are parsed into ints", func() {
				So(err, ShouldBeNil)
				So(sql, ShouldEqual, `("age" >= $1 AND "age" < $2)`)
				So(args, ShouldResemble, []any{18, 65})
			})
		})

		Convey("When a list parameter is comma-separated and repeated", func() {
			var f bindFilter
			err := Bind(url.Values{"role": {"admin,mod", "owner"}}, &f)
			c, _ := Where(f)
			sql, args := Build(c, Postgres)
			Convey("Then all values populate the IN clause", func() {
				So(err, ShouldBeNil)
				So(sql, ShouldEqual, `"role" IN ($1, $2, $3)`)
				So(args, ShouldResemble, []any{"admin", "mod", "owner"})
			})
		})

		Convey("When a scalar Opt parameter is set", func() {
			var f bindFilter
			err := Bind(url.Values{"active": {"true"}}, &f)
			c, _ := Where(f)
			sql, args := Build(c, Postgres)
			Convey("Then it parses to a bool predicate", func() {
				So(err, ShouldBeNil)
				So(sql, ShouldEqual, `"active" = $1`)
				So(args, ShouldResemble, []any{true})
			})
		})

		Convey("When several parameters combine across fields", func() {
			var f bindFilter
			vals := url.Values{
				"status":    {"active"},
				"name_like": {"A%"},
				"age_gte":   {"21"},
				"role":      {"admin"},
			}
			err := Bind(vals, &f)
			c, _ := Where(f)
			sql, args := Build(c, Postgres)
			Convey("Then every parameter contributes its predicate", func() {
				So(err, ShouldBeNil)
				So(sql, ShouldEqual,
					`("status" = $1 AND "name" LIKE $2 AND "age" >= $3 AND "role" IN ($4))`)
				So(args, ShouldResemble, []any{"active", "A%", 21, "admin"})
			})
		})

		Convey("When unrelated parameters are present", func() {
			var f bindFilter
			err := Bind(url.Values{"page": {"2"}, "sort": {"name"}, "status": {"x"}}, &f)
			Convey("Then they are ignored without error", func() {
				So(err, ShouldBeNil)
				So(f.Status.Eq.IsSet(), ShouldBeTrue)
			})
		})

		Convey("When a value cannot parse for its type", func() {
			var f bindFilter
			err := Bind(url.Values{"age_gte": {"notanumber"}}, &f)
			Convey("Then Bind returns an error", func() {
				So(err, ShouldNotBeNil)
			})
		})

		Convey("When dest is not a pointer to a struct", func() {
			err := Bind(url.Values{}, bindFilter{})
			Convey("Then Bind returns an error", func() {
				So(err, ShouldNotBeNil)
			})
		})
	})
}

func TestBindPage(t *testing.T) {
	Convey("Given pagination parameters in a query string", t, func() {

		Convey("When first and total are set", func() {
			p, err := BindPage(url.Values{"first": {"20"}, "total": {"true"}})
			Convey("Then they populate the paging params", func() {
				So(err, ShouldBeNil)
				So(*p.First, ShouldEqual, 20)
				So(p.IncludeTotal, ShouldBeTrue)
				So(p.Last, ShouldBeNil)
			})
		})

		Convey("When last and after are set", func() {
			p, err := BindPage(url.Values{"last": {"5"}, "after": {"40"}})
			Convey("Then both are parsed and total stays off", func() {
				So(err, ShouldBeNil)
				So(*p.Last, ShouldEqual, 5)
				So(*p.After, ShouldEqual, 40)
				So(p.IncludeTotal, ShouldBeFalse)
			})
		})

		Convey("When no pagination parameters are present", func() {
			p, err := BindPage(url.Values{"status": {"x"}})
			Convey("Then the zero params select everything", func() {
				So(err, ShouldBeNil)
				So(p.First, ShouldBeNil)
				So(p.Last, ShouldBeNil)
			})
		})

		Convey("When a numeric parameter is malformed", func() {
			_, err := BindPage(url.Values{"first": {"lots"}})
			Convey("Then it returns an error", func() {
				So(err, ShouldNotBeNil)
			})
		})
	})
}
