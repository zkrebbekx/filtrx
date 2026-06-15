package filtrx

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestArrayJSONOps(t *testing.T) {
	Convey("Given the PostgreSQL containment and overlap builders", t, func() {
		c := And(
			Contains("tags", []string{"go"}),
			ContainedBy("scopes", []string{"read", "write"}),
			Overlaps("roles", []string{"admin"}),
		)
		Convey("When built for Postgres", func() {
			sql, args := Build(c, Postgres)
			Convey("Then each emits its operator with a single bound argument", func() {
				So(sql, ShouldEqual, `("tags" @> $1 AND "scopes" <@ $2 AND "roles" && $3)`)
				So(len(args), ShouldEqual, 3)
			})
		})
	})

	Convey("Given a struct field tagged with an array operator", t, func() {
		type f struct {
			Tags Opt[string] `col:"tags" op:"overlaps"`
		}
		c, err := Where(f{Tags: Some("{go,rust}")})
		Convey("When compiled", func() {
			sql, _ := Build(c, Postgres)
			Convey("Then the op tag resolves to the overlap operator", func() {
				So(err, ShouldBeNil)
				So(sql, ShouldEqual, `"tags" && $1`)
			})
		})
	})
}
