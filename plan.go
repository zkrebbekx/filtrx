package filtrx

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
)

// Where compiles a tagged filter struct into a condition tree. The struct's
// field types and tags fix the legal columns and operators, so neither a column
// nor an operator can ever originate from request data — the whitelist is the
// struct definition itself. Pass a struct or a pointer to one.
//
// Field roles are inferred from type and tags:
//
//   - A field whose type implements Predicate (Range, Match, Text, or your own)
//     contributes that holder's conditions for its column.
//   - An Opt[T] field contributes a single condition using the operator from its
//     `op` tag (default eq).
//   - A non-empty slice field (without a group tag) contributes an IN condition,
//     or NOT IN with `op:"nin"`.
//   - A slice-of-struct field tagged `group:"and"` or `group:"or"` recurses,
//     joining its elements with the named connective inside parentheses.
//
// The column for a field comes from its `col` tag, then its `db` tag (so filter
// structs can double as sqlx scan targets), then the snake_case of the field
// name. A `col:"-"` tag skips the field.
//
// All set fields of the struct are AND-joined. A struct with nothing set
// compiles to a nil Cond, which Build renders as an empty WHERE.
func Where(filter any) (Cond, error) {
	v := reflect.ValueOf(filter)
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil, nil
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil, fmt.Errorf("filtrx: Where requires a struct, got %s", v.Kind())
	}
	p, err := planFor(v.Type())
	if err != nil {
		return nil, err
	}
	return p.build(v), nil
}

type stepKind int

const (
	stepPredicate stepKind = iota // field type implements Predicate
	stepScalar                    // Opt[T] field, single operator
	stepSlice                     // slice field, IN / NOT IN
	stepGroup                     // slice-of-struct field, And/Or recursion
)

type fieldStep struct {
	idx   int
	kind  stepKind
	col   string
	op    op
	or    bool
	elemT reflect.Type
}

type plan struct {
	steps []fieldStep
}

var (
	planCache  sync.Map // reflect.Type -> planResult
	predicateT = reflect.TypeOf((*Predicate)(nil)).Elem()
	setterT    = reflect.TypeOf((*setter)(nil)).Elem()
)

type planResult struct {
	p   *plan
	err error
}

func planFor(t reflect.Type) (*plan, error) {
	if r, ok := planCache.Load(t); ok {
		res := r.(planResult)
		return res.p, res.err
	}
	p, err := buildPlan(t)
	planCache.Store(t, planResult{p, err})
	return p, err
}

func buildPlan(t reflect.Type) (*plan, error) {
	p := &plan{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		col := columnName(f)
		if col == "-" {
			continue
		}
		groupTag := f.Tag.Get("group")
		switch {
		case groupTag != "":
			if f.Type.Kind() != reflect.Slice || f.Type.Elem().Kind() != reflect.Struct {
				return nil, fmt.Errorf("filtrx: field %s has group tag but is not a slice of struct", f.Name)
			}
			or, err := groupOr(groupTag, f.Name)
			if err != nil {
				return nil, err
			}
			p.steps = append(p.steps, fieldStep{idx: i, kind: stepGroup, or: or, elemT: f.Type.Elem()})
		case f.Type.Implements(predicateT):
			p.steps = append(p.steps, fieldStep{idx: i, kind: stepPredicate, col: col})
		case f.Type.Implements(setterT):
			o, err := scalarOp(f)
			if err != nil {
				return nil, err
			}
			p.steps = append(p.steps, fieldStep{idx: i, kind: stepScalar, col: col, op: o})
		case f.Type.Kind() == reflect.Slice:
			o := opIn
			if w := f.Tag.Get("op"); w != "" {
				resolved, ok := opWords[w]
				if !ok || (resolved != opIn && resolved != opNotIn) {
					return nil, fmt.Errorf("filtrx: field %s slice op %q must be in or nin", f.Name, w)
				}
				o = resolved
			}
			p.steps = append(p.steps, fieldStep{idx: i, kind: stepSlice, col: col, op: o})
		default:
			if hasFilterTag(f) {
				return nil, fmt.Errorf("filtrx: field %s has a filtrx tag but type %s is not a supported filter field", f.Name, f.Type)
			}
		}
	}
	return p, nil
}

func (p *plan) build(v reflect.Value) Cond {
	var conds []Cond
	for _, s := range p.steps {
		fv := v.Field(s.idx)
		switch s.kind {
		case stepPredicate:
			conds = append(conds, fv.Interface().(Predicate).Predicates(s.col)...)
		case stepScalar:
			if set := fv.Interface().(setter); set.isSet() {
				conds = append(conds, leaf{s.col, s.op, set.value()})
			}
		case stepSlice:
			if fv.Len() > 0 {
				conds = append(conds, leaf{s.col, s.op, fv.Interface()})
			}
		case stepGroup:
			sub, err := planFor(s.elemT)
			if err != nil {
				continue // validated at plan time for the parent's own fields
			}
			var children []Cond
			for j := 0; j < fv.Len(); j++ {
				if c := sub.build(fv.Index(j)); c != nil {
					children = append(children, c)
				}
			}
			if len(children) > 0 {
				conds = append(conds, group{or: s.or, conds: children})
			}
		}
	}
	if len(conds) == 0 {
		return nil
	}
	if len(conds) == 1 {
		return conds[0]
	}
	return group{or: false, conds: conds}
}

// columnName resolves a field's SQL column from col, then db, then snake_case.
func columnName(f reflect.StructField) string {
	if c := f.Tag.Get("col"); c != "" {
		return c
	}
	if d := f.Tag.Get("db"); d != "" {
		return strings.Split(d, ",")[0] // tolerate sqlx options like db:"name,omitempty"
	}
	return snake(f.Name)
}

func scalarOp(f reflect.StructField) (op, error) {
	w := f.Tag.Get("op")
	if w == "" {
		return opEq, nil
	}
	o, ok := opWords[w]
	if !ok {
		return "", fmt.Errorf("filtrx: field %s has unknown op %q", f.Name, w)
	}
	return o, nil
}

func groupOr(tag, field string) (bool, error) {
	switch strings.ToLower(tag) {
	case "or":
		return true, nil
	case "and":
		return false, nil
	default:
		return false, fmt.Errorf("filtrx: field %s group tag %q must be and or or", field, tag)
	}
}

func hasFilterTag(f reflect.StructField) bool {
	_, col := f.Tag.Lookup("col")
	_, o := f.Tag.Lookup("op")
	_, g := f.Tag.Lookup("group")
	return col || o || g
}

// snake converts an exported Go field name to snake_case for a default column.
// It keeps acronyms intact: an underscore is inserted before an uppercase letter
// only at a word boundary — after a lowercase letter or digit, or before the
// final letter of an acronym that starts a new word. So ID→id, UserID→user_id,
// HTTPStatus→http_status, OAuth2Token→o_auth2_token.
func snake(name string) string {
	var b strings.Builder
	r := []rune(name)
	for i, c := range r {
		if c >= 'A' && c <= 'Z' {
			prevLowerOrDigit := i > 0 && (isLower(r[i-1]) || isDigit(r[i-1]))
			nextLower := i+1 < len(r) && isLower(r[i+1])
			if i > 0 && (prevLowerOrDigit || nextLower) {
				b.WriteByte('_')
			}
			b.WriteRune(c - 'A' + 'a')
		} else {
			b.WriteRune(c)
		}
	}
	return b.String()
}

func isLower(r rune) bool { return r >= 'a' && r <= 'z' }
func isDigit(r rune) bool { return r >= '0' && r <= '9' }
