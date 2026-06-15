package filtrx

import (
	"net/url"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

type articleFilter struct {
	Body FullText `col:"search_vec"`
}

func TestFullText(t *testing.T) {
	Convey("Given a FullText filter with a query", t, func() {
		f := articleFilter{Body: FullText{Query: Some("fast car")}}
		c, _, err := compileFilter(f)
		So(err, ShouldBeNil)

		Convey("When built for Postgres", func() {
			sql, args := Build(c, Postgres)
			Convey("Then it uses websearch_to_tsquery with the bound search string", func() {
				So(sql, ShouldEqual, `"search_vec" @@ websearch_to_tsquery('english', $1)`)
				So(args, ShouldResemble, []any{"fast car"})
			})
		})

		Convey("When built for MySQL", func() {
			sql, _ := Build(c, MySQL)
			Convey("Then it uses MATCH ... AGAINST", func() {
				So(sql, ShouldEqual, "MATCH(`search_vec`) AGAINST(? IN NATURAL LANGUAGE MODE)")
			})
		})

		Convey("When built for SQLite", func() {
			sql, _ := Build(c, SQLite)
			Convey("Then it uses the FTS5 MATCH operator", func() {
				So(sql, ShouldEqual, `"search_vec" MATCH ?`)
			})
		})
	})

	Convey("Given a FullText with a custom Postgres config", t, func() {
		f := articleFilter{Body: FullText{Query: Some("rapide"), Config: "french"}}
		c, _, _ := compileFilter(f)
		Convey("When built for Postgres", func() {
			sql, _ := Build(c, Postgres)
			Convey("Then the configuration is used", func() {
				So(sql, ShouldEqual, `"search_vec" @@ websearch_to_tsquery('french', $1)`)
			})
		})
	})

	Convey("Given an unset FullText", t, func() {
		c, _, _ := compileFilter(articleFilter{})
		Convey("When built", func() {
			sql, _ := Build(c, Postgres)
			Convey("Then it contributes nothing", func() {
				So(sql, ShouldEqual, "")
			})
		})
	})

	Convey("Given a FullText filled from a query string", t, func() {
		var f articleFilter
		v, _ := url.ParseQuery("search_vec=fast+car")
		err := Bind(v, &f)
		Convey("When bound and built", func() {
			c, _, _ := compileFilter(f)
			sql, args := Build(c, Postgres)
			Convey("Then the bare wire value becomes the search query", func() {
				So(err, ShouldBeNil)
				So(sql, ShouldEqual, `"search_vec" @@ websearch_to_tsquery('english', $1)`)
				So(args, ShouldResemble, []any{"fast car"})
			})
		})
	})
}
