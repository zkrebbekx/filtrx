package filtrx

import (
	"net/url"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestBindSeek(t *testing.T) {
	Convey("Given a keyset query string", t, func() {
		v, _ := url.ParseQuery("after=Y3Vyc29y&size=20&total=true")
		p, err := BindSeek(v)
		Convey("When bound", func() {
			Convey("Then cursor, size and total are filled", func() {
				So(err, ShouldBeNil)
				So(p.After, ShouldEqual, Cursor("Y3Vyc29y"))
				So(p.Before, ShouldBeEmpty)
				So(p.Size, ShouldEqual, 20)
				So(p.IncludeTotal, ShouldBeTrue)
			})
		})
	})

	Convey("Given a backward keyset query string with include_total", t, func() {
		v, _ := url.ParseQuery("before=cHJldg&size=5&include_total=1")
		p, err := BindSeek(v)
		Convey("When bound", func() {
			Convey("Then the before cursor and total flag are read", func() {
				So(err, ShouldBeNil)
				So(p.Before, ShouldEqual, Cursor("cHJldg"))
				So(p.IncludeTotal, ShouldBeTrue)
			})
		})
	})

	Convey("Given a non-integer size", t, func() {
		v, _ := url.ParseQuery("size=lots")
		_, err := BindSeek(v)
		Convey("When bound", func() {
			Convey("Then it returns an error", func() {
				So(err, ShouldNotBeNil)
			})
		})
	})
}
