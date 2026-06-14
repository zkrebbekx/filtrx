package filtrx_test

import (
	"fmt"

	"github.com/zkrebbekx/filtrx"
)

// A filter struct is decorated once and then filled straight from a request.
type userFilter struct {
	Status filtrx.Text       `col:"status"`
	Name   filtrx.Text       `col:"name"`
	Age    filtrx.Range[int] `col:"age"`
	Roles  []string          `col:"role" op:"in"`
	Any    []userFilter      `group:"or"`
}

// Compile a tagged struct into SQL.
func ExampleWhere() {
	f := userFilter{
		Status: filtrx.Text{Eq: filtrx.Some("active")},
		Age:    filtrx.Range[int]{Gte: filtrx.Some(18), Lt: filtrx.Some(65)},
		Roles:  []string{"admin", "mod"},
	}

	cond, _ := filtrx.Where(f)
	sql, args := filtrx.Build(cond, filtrx.Postgres)

	fmt.Println(sql)
	fmt.Println(args...)
	// Output:
	// ("status" = $1 AND "age" >= $2 AND "age" < $3 AND "role" IN ($4, $5))
	// active 18 65 admin mod
}

// Nested OR groups bracket exactly.
func ExampleWhere_nested() {
	f := userFilter{
		Status: filtrx.Text{Eq: filtrx.Some("active")},
		Any: []userFilter{
			{Age: filtrx.Range[int]{Gt: filtrx.Some(18)}},
			{Name: filtrx.Text{Like: filtrx.Some("A%")}},
		},
	}

	cond, _ := filtrx.Where(f)
	sql, _ := filtrx.Build(cond, filtrx.Postgres)

	fmt.Println(sql)
	// Output:
	// ("status" = $1 AND ("age" > $2 OR "name" LIKE $3))
}

// Build a condition by hand with the constructor API.
func ExampleAnd() {
	cond := filtrx.And(
		filtrx.Eq("status", "active"),
		filtrx.Or(filtrx.Gt("age", 18), filtrx.IsNull("deleted_at")),
	)
	sql, _ := filtrx.Build(cond, filtrx.MySQL)
	fmt.Println(sql)
	// Output:
	// (`status` = ? AND (`age` > ? OR `deleted_at` IS NULL))
}

// Resolve pagination into a limit and offset.
func ExamplePaginate() {
	first := 10
	paginator, needsTotal := filtrx.Paginate(filtrx.PagingParams{First: &first})
	limit, offset := paginator(0)
	fmt.Println(limit, offset, needsTotal)
	// Output:
	// 10 0 false
}
