package filtrx

// Predicate is implemented by a filter field type that contributes one or more
// conditions for the column it is tagged with. The built-in holders Range,
// Match and Text implement it; you can define your own holder for a column type
// with special needs (PostGIS, JSON containment, full-text) and filtrx will
// treat it exactly like the built-ins.
//
// Predicates must return conditions only for the operators that are actually
// set, and nothing for an unset (zero-value) holder.
type Predicate interface {
	// Predicates returns the conditions this holder contributes for column col.
	Predicates(col string) []Cond
}

// Range is a filter holder for an ordered column — numbers, time.Time, dates,
// decimals, anything the database can compare with the ordering operators. The
// element type is unconstrained because filtrx never orders values in Go; it
// only emits "col > $1" and lets the database order them. Set any subset of its
// fields; only the set ones become conditions, AND-joined. Its zero value
// contributes nothing, and one Range covers "between" via Gte plus Lte.
type Range[T any] struct {
	Eq     Opt[T]    `json:"eq,omitempty"`
	Ne     Opt[T]    `json:"ne,omitempty"`
	Gt     Opt[T]    `json:"gt,omitempty"`
	Lt     Opt[T]    `json:"lt,omitempty"`
	Gte    Opt[T]    `json:"gte,omitempty"`
	Lte    Opt[T]    `json:"lte,omitempty"`
	In     []T       `json:"in,omitempty"`
	IsNull Opt[bool] `json:"null,omitempty"`
}

// Predicates implements Predicate.
func (r Range[T]) Predicates(col string) []Cond {
	var c []Cond
	c = appendOpt(c, col, opEq, r.Eq)
	c = appendOpt(c, col, opNe, r.Ne)
	c = appendOpt(c, col, opGt, r.Gt)
	c = appendOpt(c, col, opGte, r.Gte)
	c = appendOpt(c, col, opLt, r.Lt)
	c = appendOpt(c, col, opLte, r.Lte)
	c = appendIn(c, col, r.In)
	return appendNull(c, col, r.IsNull)
}

// Match is a filter holder for a column compared only by equality and set
// membership — booleans, enums, UUIDs, any comparable type that has no ordering.
type Match[T comparable] struct {
	Eq     Opt[T]    `json:"eq,omitempty"`
	Ne     Opt[T]    `json:"ne,omitempty"`
	In     []T       `json:"in,omitempty"`
	IsNull Opt[bool] `json:"null,omitempty"`
}

// Predicates implements Predicate.
func (m Match[T]) Predicates(col string) []Cond {
	var c []Cond
	c = appendOpt(c, col, opEq, m.Eq)
	c = appendOpt(c, col, opNe, m.Ne)
	c = appendIn(c, col, m.In)
	return appendNull(c, col, m.IsNull)
}

// Text is a filter holder for string columns. Beyond equality it offers pattern
// matching: Like is case-sensitive, ILike case-insensitive (emitted as ILIKE on
// Postgres; pair it with a lowered column elsewhere).
type Text struct {
	Eq     Opt[string] `json:"eq,omitempty"`
	Ne     Opt[string] `json:"ne,omitempty"`
	Like   Opt[string] `json:"like,omitempty"`
	ILike  Opt[string] `json:"ilike,omitempty"`
	In     []string    `json:"in,omitempty"`
	IsNull Opt[bool]   `json:"null,omitempty"`
}

// Predicates implements Predicate.
func (t Text) Predicates(col string) []Cond {
	var c []Cond
	c = appendOpt(c, col, opEq, t.Eq)
	c = appendOpt(c, col, opNe, t.Ne)
	c = appendOpt(c, col, opLike, t.Like)
	c = appendOpt(c, col, opILike, t.ILike)
	c = appendIn(c, col, t.In)
	return appendNull(c, col, t.IsNull)
}

func appendOpt[T any](c []Cond, col string, o op, v Opt[T]) []Cond {
	if val, ok := v.Get(); ok {
		return append(c, leaf{col, o, val})
	}
	return c
}

func appendIn[T any](c []Cond, col string, vals []T) []Cond {
	if len(vals) > 0 {
		return append(c, leaf{col, opIn, vals})
	}
	return c
}

func appendNull(c []Cond, col string, o Opt[bool]) []Cond {
	if want, ok := o.Get(); ok {
		if want {
			return append(c, leaf{col, opNull, nil})
		}
		return append(c, leaf{col, opNNull, nil})
	}
	return c
}
