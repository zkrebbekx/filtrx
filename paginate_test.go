package filtrx

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func intp(n int) *int { return &n }

func TestPaginate(t *testing.T) {
	Convey("Given paging parameters", t, func() {

		Convey("When only First is set", func() {
			fn, needsTotal := Paginate(PagingParams{First: intp(10)})
			Convey("Then no pre-count is needed and limit/offset are first/0", func() {
				So(needsTotal, ShouldBeFalse)
				So(fn, ShouldNotBeNil)
				limit, offset := fn(0)
				So(limit, ShouldEqual, 10)
				So(offset, ShouldEqual, 0)
			})
		})

		Convey("When First follows After", func() {
			fn, needsTotal := Paginate(PagingParams{First: intp(5), After: intp(20)})
			Convey("Then offset is After+1 and no pre-count is needed", func() {
				So(needsTotal, ShouldBeFalse)
				limit, offset := fn(0)
				So(limit, ShouldEqual, 5)
				So(offset, ShouldEqual, 21)
			})
		})

		Convey("When Last is set", func() {
			fn, needsTotal := Paginate(PagingParams{Last: intp(10)})
			Convey("Then a pre-count is required to resolve the offset from the end", func() {
				So(needsTotal, ShouldBeTrue)
				So(fn, ShouldNotBeNil)
			})
			Convey("And the window sits at the end of the record set", func() {
				limit, offset := fn(100)
				So(limit, ShouldEqual, 10)
				So(offset, ShouldEqual, 90)
			})
			Convey("And it clamps to zero when fewer records than Last exist", func() {
				limit, offset := fn(4)
				So(limit, ShouldEqual, 10)
				So(offset, ShouldEqual, 0)
			})
		})

		Convey("When First is zero", func() {
			fn, needsTotal := Paginate(PagingParams{First: intp(0), IncludeTotal: true})
			Convey("Then no paginator is returned but the total is still requested", func() {
				So(fn, ShouldBeNil)
				So(needsTotal, ShouldBeTrue)
			})
		})

		Convey("When Before bounds the window from the front", func() {
			fn, needsTotal := Paginate(PagingParams{Before: intp(8)})
			Convey("Then the window covers offsets 0..7", func() {
				So(needsTotal, ShouldBeFalse)
				limit, offset := fn(0)
				So(offset, ShouldEqual, 0)
				So(limit, ShouldEqual, 8)
			})
		})

		Convey("When Before is at or behind After", func() {
			fn, needsTotal := Paginate(PagingParams{After: intp(10), Before: intp(11)})
			Convey("Then the range is empty so no paginator is returned", func() {
				So(fn, ShouldBeNil)
				So(needsTotal, ShouldBeFalse)
			})
		})

		Convey("When Last is combined with Before", func() {
			fn, needsTotal := Paginate(PagingParams{Last: intp(3), Before: intp(10)})
			Convey("Then the window is the 3 records ending just before offset 10", func() {
				So(needsTotal, ShouldBeFalse) // Before makes the offset knowable without a count
				limit, offset := fn(0)
				So(limit, ShouldEqual, 3)
				So(offset, ShouldEqual, 7)
			})
		})

		Convey("When IncludeTotal is set on a forward page", func() {
			_, needsTotal := Paginate(PagingParams{First: intp(10), IncludeTotal: true})
			Convey("Then no pre-count is needed because the window total is used", func() {
				So(needsTotal, ShouldBeFalse)
			})
		})
	})

	Convey("Given contradictory paging parameters", t, func() {
		Convey("When both First and Last are set", func() {
			Convey("Then Paginate panics", func() {
				So(func() { Paginate(PagingParams{First: intp(1), Last: intp(1)}) }, ShouldPanic)
			})
		})
		Convey("When First is negative", func() {
			Convey("Then Paginate panics", func() {
				So(func() { Paginate(PagingParams{First: intp(-1)}) }, ShouldPanic)
			})
		})
	})

	Convey("Given TruncateAt", t, func() {
		Convey("When the limit is positive", func() {
			p := TruncateAt(25)
			Convey("Then First is set to the limit", func() {
				So(p.First, ShouldNotBeNil)
				So(*p.First, ShouldEqual, 25)
			})
		})
		Convey("When the limit is zero", func() {
			p := TruncateAt(0)
			Convey("Then the zero params select everything", func() {
				So(p.First, ShouldBeNil)
			})
		})
	})
}
