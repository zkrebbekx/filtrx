package filtrx_test

import (
	"fmt"
	"net/url"

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

// Fill a filter straight from a REST query string.
func ExampleBind() {
	var f userFilter
	vals, _ := url.ParseQuery("status=active&age_gte=18&age_lt=65&role=admin&role=mod")

	_ = filtrx.Bind(vals, &f)
	cond, _ := filtrx.Where(f)
	sql, args := filtrx.Build(cond, filtrx.Postgres)

	fmt.Println(sql)
	fmt.Println(args...)
	// Output:
	// ("status" = $1 AND "age" >= $2 AND "age" < $3 AND "role" IN ($4, $5))
	// active 18 65 admin mod
}

// Filter a base table by a one-to-many relationship without fan-out, using a
// correlated EXISTS declared on the filter struct.
func ExampleExists() {
	type orderSub struct {
		Status filtrx.Text `col:"o.status"`
	}
	type customerFilter struct {
		Base   filtrx.Table            `table:"customers" as:"c"`
		Status filtrx.Text             `col:"c.status"`
		Orders filtrx.Exists[orderSub] `exists:"orders o" on:"o.customer_id = c.id"`
	}

	f := customerFilter{
		Status: filtrx.Text{Eq: filtrx.Some("active")},
		Orders: filtrx.Exists[orderSub]{
			When: filtrx.Some(true),
			Sub:  orderSub{Status: filtrx.Text{Eq: filtrx.Some("paid")}},
		},
	}

	cond, _ := filtrx.Where(f)
	sql, _ := filtrx.Build(cond, filtrx.Postgres)
	fmt.Println(sql)
	// Output:
	// ("c"."status" = $1 AND EXISTS (SELECT 1 FROM orders o WHERE o.customer_id = c.id AND "o"."status" = $2))
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
