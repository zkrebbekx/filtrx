package filtrx

import (
	"net/url"
	"testing"
)

// benchFilter is a representative request-shaped filter.
type benchFilter struct {
	Status Text          `col:"status"`
	Name   Text          `col:"name"`
	Age    Range[int]    `col:"age"`
	Roles  []string      `col:"role" op:"in"`
	Any    []benchFilter `group:"or"`
}

func sampleFilter() benchFilter {
	return benchFilter{
		Status: Text{Eq: Some("active")},
		Age:    Range[int]{Gte: Some(18), Lt: Some(65)},
		Roles:  []string{"admin", "mod"},
		Any: []benchFilter{
			{Name: Text{Like: Some("A%")}},
			{Age: Range[int]{Gt: Some(100)}},
		},
	}
}

// BenchmarkWhere measures compiling a tagged struct to a Cond tree. The plan is
// cached per type, so this reflects the per-request cost after warm-up.
func BenchmarkWhere(b *testing.B) {
	f := sampleFilter()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Where(f); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkWhereBuild measures the full compile-and-render path.
func BenchmarkWhereBuild(b *testing.B) {
	f := sampleFilter()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		c, _ := Where(f)
		Build(c, Postgres)
	}
}

// BenchmarkBind measures populating a filter from a query string.
func BenchmarkBind(b *testing.B) {
	vals, _ := url.ParseQuery("status=active&age_gte=18&age_lt=65&role=admin&role=mod&name_like=A%25")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var f benchFilter
		if err := Bind(vals, &f); err != nil {
			b.Fatal(err)
		}
	}
}
