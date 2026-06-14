package filtrx

import (
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

type userFilter struct {
	Status Text         `col:"status"`
	Name   Text         `col:"name"`
	Age    Range[int]   `col:"age"`
	Active Opt[bool]    `col:"active" op:"eq"`
	Roles  []string     `col:"role" op:"in"`
	Any    []userFilter `group:"or"`
	All    []userFilter `group:"and"`
}

func TestWhere(t *testing.T) {
	Convey("Given a tagged filter struct", t, func() {

		Convey("When nothing is set", func() {
			c, err := Where(userFilter{})
			Convey("Then it compiles to a nil condition", func() {
				So(err, ShouldBeNil)
				So(c, ShouldBeNil)
			})
		})

		Convey("When a single holder field is set", func() {
			c, err := Where(userFilter{Status: Text{Eq: Some("active")}})
			sql, args := Build(c, Postgres)
			Convey("Then one predicate is produced for its column", func() {
				So(err, ShouldBeNil)
				So(sql, ShouldEqual, `"status" = $1`)
				So(args, ShouldResemble, []any{"active"})
			})
		})

		Convey("When a Range holder sets two bounds", func() {
			c, _ := Where(userFilter{Age: Range[int]{Gte: Some(18), Lt: Some(65)}})
			sql, args := Build(c, Postgres)
			Convey("Then both bounds AND together for the column", func() {
				So(sql, ShouldEqual, `("age" >= $1 AND "age" < $2)`)
				So(args, ShouldResemble, []any{18, 65})
			})
		})

		Convey("When scalar, slice and holder fields are all set", func() {
			c, _ := Where(userFilter{
				Status: Text{Eq: Some("active")},
				Active: Some(true),
				Roles:  []string{"admin", "mod"},
			})
			sql, args := Build(c, Postgres)
			Convey("Then all AND together in field order", func() {
				So(sql, ShouldEqual, `("status" = $1 AND "active" = $2 AND "role" IN ($3, $4))`)
				So(args, ShouldResemble, []any{"active", true, "admin", "mod"})
			})
		})

		Convey("When an OR group nests child filters", func() {
			c, _ := Where(userFilter{
				Status: Text{Eq: Some("active")},
				Any: []userFilter{
					{Age: Range[int]{Gt: Some(18)}},
					{Name: Text{Like: Some("A%")}},
				},
			})
			sql, args := Build(c, Postgres)
			Convey("Then the children are OR-joined and bracketed", func() {
				So(sql, ShouldEqual, `("status" = $1 AND ("age" > $2 OR "name" LIKE $3))`)
				So(args, ShouldResemble, []any{"active", 18, "A%"})
			})
		})

		Convey("When the same struct is compiled twice", func() {
			_, e1 := Where(userFilter{Status: Text{Eq: Some("a")}})
			_, e2 := Where(userFilter{Status: Text{Eq: Some("b")}})
			Convey("Then the cached plan serves both without error", func() {
				So(e1, ShouldBeNil)
				So(e2, ShouldBeNil)
			})
		})
	})

	Convey("Given column-name resolution", t, func() {
		Convey("When a field uses a db tag instead of col", func() {
			type f struct {
				Email Text `db:"email_address,omitempty"`
			}
			c, _ := Where(f{Email: Text{Eq: Some("x@y.z")}})
			sql, _ := Build(c, Postgres)
			Convey("Then the db tag (minus options) is the column", func() {
				So(sql, ShouldEqual, `"email_address" = $1`)
			})
		})
		Convey("When a field has no col or db tag", func() {
			type f struct {
				CreatedAt Range[int] `col:""`
			}
			c, _ := Where(f{CreatedAt: Range[int]{Gt: Some(1)}})
			sql, _ := Build(c, Postgres)
			Convey("Then the column defaults to the snake_case field name", func() {
				So(sql, ShouldEqual, `"created_at" > $1`)
			})
		})
	})

	Convey("Given invalid filter definitions", t, func() {
		Convey("When a non-struct is passed", func() {
			_, err := Where(42)
			Convey("Then an error is returned", func() {
				So(err, ShouldNotBeNil)
			})
		})
		Convey("When an Opt field has an unknown op", func() {
			type f struct {
				X Opt[int] `col:"x" op:"betwixt"`
			}
			_, err := Where(f{})
			Convey("Then compilation fails", func() {
				So(err, ShouldNotBeNil)
			})
		})
		Convey("When a group tag is on a non-slice field", func() {
			type f struct {
				X Opt[int] `group:"or"`
			}
			_, err := Where(f{})
			Convey("Then compilation fails", func() {
				So(err, ShouldNotBeNil)
			})
		})
	})

	Convey("Given a filter populated from JSON", t, func() {
		Convey("When a request body is unmarshalled into the struct", func() {
			var f userFilter
			body := `{"Status":{"eq":"active"},"Age":{"gte":18},"Roles":["admin"]}`
			err := json.Unmarshal([]byte(body), &f)
			c, _ := Where(f)
			sql, args := Build(c, Postgres)
			Convey("Then present keys become predicates and absent ones are skipped", func() {
				So(err, ShouldBeNil)
				So(sql, ShouldEqual, `("status" = $1 AND "age" >= $2 AND "role" IN ($3))`)
				So(args, ShouldResemble, []any{"active", 18, "admin"})
			})
		})
	})
}
