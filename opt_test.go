package filtrx

import (
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestOpt(t *testing.T) {
	Convey("Given an Opt value", t, func() {

		Convey("When created with Some", func() {
			o := Some(7)
			Convey("Then it is set and yields its value", func() {
				v, ok := o.Get()
				So(ok, ShouldBeTrue)
				So(v, ShouldEqual, 7)
				So(o.IsSet(), ShouldBeTrue)
			})
		})

		Convey("When it is the zero value", func() {
			var o Opt[string]
			Convey("Then it is unset and Or falls back to the default", func() {
				So(o.IsSet(), ShouldBeFalse)
				So(o.Or("fallback"), ShouldEqual, "fallback")
			})
		})

		Convey("When it carries a genuine zero value", func() {
			o := Some(0)
			Convey("Then it is still distinguishable from unset", func() {
				So(o.IsSet(), ShouldBeTrue)
				v, _ := o.Get()
				So(v, ShouldEqual, 0)
			})
		})
	})

	Convey("Given JSON round-tripping", t, func() {
		type payload struct {
			A Opt[int] `json:"a"`
			B Opt[int] `json:"b"`
		}
		Convey("When a key is present and another absent", func() {
			var p payload
			err := json.Unmarshal([]byte(`{"a":5}`), &p)
			Convey("Then presence is detected exactly", func() {
				So(err, ShouldBeNil)
				So(p.A.IsSet(), ShouldBeTrue)
				So(p.B.IsSet(), ShouldBeFalse)
			})
		})
		Convey("When a key is explicitly null", func() {
			var p payload
			err := json.Unmarshal([]byte(`{"a":null}`), &p)
			Convey("Then it is treated as unset", func() {
				So(err, ShouldBeNil)
				So(p.A.IsSet(), ShouldBeFalse)
			})
		})
		Convey("When marshalling a set and an unset Opt", func() {
			b, _ := json.Marshal(payload{A: Some(9)})
			Convey("Then the set value encodes and the unset becomes null", func() {
				So(string(b), ShouldEqual, `{"a":9,"b":null}`)
			})
		})
	})
}
