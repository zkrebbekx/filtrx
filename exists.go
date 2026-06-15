package filtrx

import "reflect"

// Exists is a filter holder for a one-to-many relationship, compiled to a
// correlated EXISTS subquery rather than a join. Where a Join multiplies the
// base row once per matching child — fanning the result out and corrupting the
// page total — an Exists tests for the presence of a matching child without
// touching the cardinality of the result. Use it to filter a base table by its
// "many" side: users that have any paid order, products with a five-star
// review.
//
// The subquery's source table and its correlation are declared on the field's
// tags; the nested filter struct T supplies the additional predicates on the
// child table:
//
//	type UserFilter struct {
//		Base   filtrx.Table             `table:"users" as:"u"`
//		Orders filtrx.Exists[OrderSub]  `exists:"orders o" on:"o.user_id = u.id"`
//	}
//	type OrderSub struct {
//		Status filtrx.Text       `col:"o.status"`
//		Total  filtrx.Range[int] `col:"o.total"`
//	}
//
// When is the toggle, so the field reads cleanly from a request: leave it unset
// to ignore the relationship entirely, Some(true) for EXISTS, Some(false) for
// NOT EXISTS. Sub is applied only when When is set.
//
//	UserFilter{Orders: filtrx.Exists[OrderSub]{
//		When: filtrx.Some(true),
//		Sub:  OrderSub{Status: filtrx.Text{Eq: filtrx.Some("paid")}},
//	}}
//	// → EXISTS (SELECT 1 FROM orders o WHERE o.user_id = u.id AND "o"."status" = $1)
//
// The exists and on tags are emitted verbatim (they are developer-authored, not
// request input), exactly like a Join's on; never build them from untrusted
// data. Child-column predicates come from Sub and are parameterised as usual.
type Exists[T any] struct {
	When Opt[bool]
	Sub  T
}

// existsActive reports whether the field contributes a condition and, if so,
// whether it is negated (NOT EXISTS). It is the type-erased read the compiler
// uses without knowing T.
func (e Exists[T]) existsActive() (active, negate bool) {
	want, ok := e.When.Get()
	if !ok {
		return false, false
	}
	return true, !want
}

// existsHolder is implemented by every Exists[T]. It lets the plan classify the
// field and read its toggle without reflecting into the generic's fields.
type existsHolder interface {
	existsActive() (active, negate bool)
}

var existsHolderT = reflect.TypeOf((*existsHolder)(nil)).Elem()

// exists is a correlated EXISTS leaf: [NOT] EXISTS (SELECT 1 FROM src WHERE on
// [AND inner]). src and on are verbatim developer-authored SQL; inner is the
// compiled sub-filter and continues the surrounding bind numbering.
type exists struct {
	negate bool
	src    string
	on     string
	inner  Cond
}

func (e exists) write(b *builder) {
	if e.negate {
		b.sql.WriteString("NOT ")
	}
	b.sql.WriteString("EXISTS (SELECT 1 FROM ")
	b.sql.WriteString(e.src)
	b.sql.WriteString(" WHERE ")
	b.sql.WriteString(e.on)
	if e.inner != nil {
		b.sql.WriteString(" AND ")
		e.inner.write(b)
	}
	b.sql.WriteByte(')')
}
