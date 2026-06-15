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
	c, _, err := compileFilter(filter)
	return c, err
}

// compileFilter resolves a filter struct to its condition and the source
// (FROM/joins) declared by its marker fields, if any. It backs both the public
// Where and the Query builder's Where, exposing only the narrow source seam
// rather than the whole plan. The returned *sourceSpec is the cached, read-only
// spec shared across all queries of this filter type; callers must not mutate it.
func compileFilter(filter any) (Cond, *sourceSpec, error) {
	v := reflect.ValueOf(filter)
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil, nil, nil
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil, nil, fmt.Errorf("filtrx: Where requires a struct, got %s", v.Kind())
	}
	p, err := planFor(v.Type())
	if err != nil {
		return nil, nil, err
	}
	return p.build(v), p.source, nil
}

type stepKind int

const (
	stepPredicate stepKind = iota // field type implements Predicate
	stepScalar                    // Opt[T] field, single operator
	stepSlice                     // slice field, IN / NOT IN
	stepGroup                     // slice-of-struct field, And/Or recursion
	stepExists                    // Exists[T] field, correlated EXISTS subquery
)

type fieldStep struct {
	idx    int
	kind   stepKind
	col    string
	op     op
	or     bool
	elemT  reflect.Type
	src    string // stepExists: verbatim subquery source ("orders o")
	on     string // stepExists: verbatim correlation ("o.user_id = u.id")
	subIdx int    // stepExists: index of the Sub field within Exists[T]
}

type plan struct {
	steps  []fieldStep
	source *sourceSpec // FROM/joins from marker fields; nil for single-table
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

// fieldRole is the kind of filter a struct field represents. It is the single
// classification shared by the Where compiler and the query-string Bind, so a
// field means the same thing whether it is filled in code or from a request.
type fieldRole int

const (
	roleNone      fieldRole = iota // not a filter field
	roleGroup                      // slice-of-struct with a group tag (And/Or)
	roleExists                     // Exists[T], a correlated EXISTS subquery
	rolePredicate                  // type implements Predicate (Range/Match/Text)
	roleScalar                     // Opt[T], a single operator
	roleSlice                      // slice, IN / NOT IN
)

// classifyField determines a field's role from its type and tags.
func classifyField(f reflect.StructField) fieldRole {
	if !f.IsExported() {
		return roleNone
	}
	if f.Tag.Get("group") != "" {
		return roleGroup
	}
	switch {
	case f.Type.Implements(existsHolderT):
		return roleExists
	case f.Type.Implements(predicateT):
		return rolePredicate
	case f.Type.Implements(setterT):
		return roleScalar
	case f.Type.Kind() == reflect.Slice:
		return roleSlice
	default:
		return roleNone
	}
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

		// Source markers (Table/Join) declare the table and joins, not a filter.
		// Like filter fields they must be exported to be read.
		if f.IsExported() && (f.Type == tableType || f.Type == joinType) {
			if p.source == nil {
				p.source = &sourceSpec{}
			}
			if f.Type == tableType {
				if err := parseTable(f, p.source); err != nil {
					return nil, err
				}
			} else {
				j, err := parseJoin(f)
				if err != nil {
					return nil, err
				}
				p.source.joins = append(p.source.joins, j)
			}
			continue
		}

		role := classifyField(f)
		if role == roleNone {
			if f.IsExported() && hasFilterTag(f) {
				return nil, fmt.Errorf("filtrx: field %s has a filtrx tag but type %s is not a supported filter field", f.Name, f.Type)
			}
			continue
		}
		col := columnName(f)
		if col == "-" {
			continue
		}
		switch role {
		case roleGroup:
			if f.Type.Kind() != reflect.Slice || f.Type.Elem().Kind() != reflect.Struct {
				return nil, fmt.Errorf("filtrx: field %s has group tag but is not a slice of struct", f.Name)
			}
			or, err := groupOr(f.Tag.Get("group"), f.Name)
			if err != nil {
				return nil, err
			}
			p.steps = append(p.steps, fieldStep{idx: i, kind: stepGroup, or: or, elemT: f.Type.Elem()})
		case roleExists:
			step, err := existsStep(f, i)
			if err != nil {
				return nil, err
			}
			p.steps = append(p.steps, step)
		case rolePredicate:
			p.steps = append(p.steps, fieldStep{idx: i, kind: stepPredicate, col: col})
		case roleScalar:
			o, err := scalarOp(f)
			if err != nil {
				return nil, err
			}
			p.steps = append(p.steps, fieldStep{idx: i, kind: stepScalar, col: col, op: o})
		case roleSlice:
			o := opIn
			if w := f.Tag.Get("op"); w != "" {
				resolved, ok := opWords[w]
				if !ok || (resolved != opIn && resolved != opNotIn) {
					return nil, fmt.Errorf("filtrx: field %s slice op %q must be in or nin", f.Name, w)
				}
				o = resolved
			}
			p.steps = append(p.steps, fieldStep{idx: i, kind: stepSlice, col: col, op: o})
		}
	}
	if p.source != nil && p.source.table == "" {
		return nil, fmt.Errorf("filtrx: filter declares Join fields but no Table field")
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
		case stepExists:
			active, negate := fv.Interface().(existsHolder).existsActive()
			if !active {
				continue
			}
			sub, err := planFor(s.elemT)
			if err != nil {
				continue // validated at plan time
			}
			conds = append(conds, exists{
				negate: negate,
				src:    s.src,
				on:     s.on,
				inner:  sub.build(fv.Field(s.subIdx)),
			})
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

// existsStep validates an Exists[T] field and compiles its plan step. The
// subquery source and correlation come from the exists and on tags; the nested
// filter type T is validated (and cached) up front so a malformed child surfaces
// at plan time, not at query time.
func existsStep(f reflect.StructField, idx int) (fieldStep, error) {
	src := f.Tag.Get("exists")
	if src == "" {
		return fieldStep{}, fmt.Errorf("filtrx: Exists field %s needs an exists tag naming the subquery table", f.Name)
	}
	on := f.Tag.Get("on")
	if on == "" {
		return fieldStep{}, fmt.Errorf("filtrx: Exists field %s needs an on tag correlating the subquery", f.Name)
	}
	subField, ok := f.Type.FieldByName("Sub")
	if !ok || subField.Type.Kind() != reflect.Struct {
		return fieldStep{}, fmt.Errorf("filtrx: Exists field %s has a non-struct Sub filter", f.Name)
	}
	if _, err := planFor(subField.Type); err != nil {
		return fieldStep{}, err
	}
	return fieldStep{idx: idx, kind: stepExists, src: src, on: on, elemT: subField.Type, subIdx: subField.Index[0]}, nil
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
	_, e := f.Tag.Lookup("exists")
	return col || o || g || e
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
