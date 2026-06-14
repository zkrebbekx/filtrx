package filtrx

import (
	"fmt"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// Bind populates a filter struct from URL query parameters, so a REST handler
// can turn a raw query string into a ready-to-compile filter with no manual
// parsing. Pass a pointer to the filter struct.
//
// Each field's parameter name is its `q` tag, then its `col` tag, then the
// snake_case of the field name ("wire name"). Values are matched as follows:
//
//   - A holder field (Range, Match, Text) reads wire_<op> for each operator —
//     age_gte=18, age_lt=65, name_like=A%25 — and a bare wire as equality
//     (status=active). The IN operator takes a repeated or comma-separated value
//     (role_in=a,b or role=a&role=b).
//   - An Opt[T] field reads its bare wire name, parsed with the field's operator.
//   - A slice field reads a repeated or comma-separated wire name as its list.
//
// Nested group fields are not bound from query strings; build those from a JSON
// body or in code. Unknown parameters are ignored, so pagination and sort params
// can share the query string. A value that fails to parse for its target type
// returns an error.
func Bind(values url.Values, dest any) error {
	v := reflect.ValueOf(dest)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return fmt.Errorf("filtrx: Bind requires a non-nil pointer to a struct")
	}
	v = v.Elem()
	if v.Kind() != reflect.Struct {
		return fmt.Errorf("filtrx: Bind requires a pointer to a struct, got %s", v.Kind())
	}
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() || f.Tag.Get("group") != "" {
			continue
		}
		wire := wireName(f)
		if wire == "-" {
			continue
		}
		if err := bindField(values, wire, f, v.Field(i)); err != nil {
			return err
		}
	}
	return nil
}

// BindPage reads pagination arguments from a query string into PagingParams.
// It recognises first, last, after and before (integers) and total (a bool,
// also accepted as include_total). Absent parameters leave their fields nil/zero,
// so the result is safe to pass straight to Query.Page. A non-integer value for
// one of the numeric parameters returns an error.
func BindPage(values url.Values) (PagingParams, error) {
	var p PagingParams
	for _, b := range []struct {
		key string
		dst **int
	}{
		{"first", &p.First}, {"last", &p.Last},
		{"after", &p.After}, {"before", &p.Before},
	} {
		if raw, ok := first(values, b.key); ok {
			n, err := strconv.Atoi(raw)
			if err != nil {
				return PagingParams{}, fmt.Errorf("filtrx: bind page %q: %w", b.key, err)
			}
			*b.dst = &n
		}
	}
	if raw, ok := first(values, "total"); ok {
		p.IncludeTotal = truthy(raw)
	} else if raw, ok := first(values, "include_total"); ok {
		p.IncludeTotal = truthy(raw)
	}
	return p, nil
}

func truthy(s string) bool {
	b, err := strconv.ParseBool(s)
	return err == nil && b
}

func bindField(values url.Values, wire string, f reflect.StructField, fv reflect.Value) error {
	switch {
	case fv.Addr().Type().Implements(stringSetterType): // scalar Opt[T]
		if raw, ok := first(values, wire); ok {
			return setScalar(fv, raw, wire)
		}
		return nil
	case fv.Kind() == reflect.Slice && f.Tag.Get("group") == "": // top-level slice → IN
		return bindSlice(values, wire, fv, wire)
	case fv.Type().Implements(predicateT): // holder
		return bindHolder(values, wire, fv)
	default:
		return nil
	}
}

// bindHolder fills a holder's operator sub-fields from wire_<op> parameters.
func bindHolder(values url.Values, wire string, fv reflect.Value) error {
	ht := fv.Type()
	for i := 0; i < ht.NumField(); i++ {
		sf := ht.Field(i)
		opword := jsonName(sf)
		if opword == "" || opword == "-" {
			continue
		}
		sv := fv.Field(i)
		if sv.Kind() == reflect.Slice { // IN
			key := wire + "_" + opword
			// No bare-wire fallback here: a bare wire is equality, not IN.
			if err := bindSlice(values, key, sv, key); err != nil {
				return err
			}
			continue
		}
		// Scalar operator: wire_<op>, plus bare wire as a shorthand for equality.
		key := wire + "_" + opword
		raw, ok := first(values, key)
		if !ok && opword == "eq" {
			raw, ok = first(values, wire)
		}
		if ok {
			if err := setScalar(sv, raw, key); err != nil {
				return err
			}
		}
	}
	return nil
}

func bindSlice(values url.Values, key string, sv reflect.Value, bareAlias string) error {
	raws := listValues(values, key)
	if len(raws) == 0 && bareAlias != key {
		raws = listValues(values, bareAlias)
	}
	if len(raws) == 0 {
		return nil
	}
	et := sv.Type().Elem()
	out := reflect.MakeSlice(sv.Type(), 0, len(raws))
	for _, r := range raws {
		ev := reflect.New(et).Elem()
		if err := parseScalar(ev, r); err != nil {
			return fmt.Errorf("filtrx: bind %q: %w", key, err)
		}
		out = reflect.Append(out, ev)
	}
	sv.Set(out)
	return nil
}

func setScalar(optOrPtr reflect.Value, raw, key string) error {
	s, ok := optOrPtr.Addr().Interface().(stringSetter)
	if !ok {
		return fmt.Errorf("filtrx: bind %q: field is not bindable", key)
	}
	if err := s.setString(raw); err != nil {
		return fmt.Errorf("filtrx: bind %q: %w", key, err)
	}
	return nil
}

// stringSetter is implemented by *Opt[T]; it parses a query-string value into the
// optional and marks it set.
type stringSetter interface {
	setString(s string) error
}

var stringSetterType = reflect.TypeOf((*stringSetter)(nil)).Elem()

// parseScalar parses s into the (settable) reflect value v according to its kind.
// It covers the primitive kinds plus time.Time (RFC 3339).
func parseScalar(v reflect.Value, s string) error {
	switch v.Kind() {
	case reflect.String:
		v.SetString(s)
	case reflect.Bool:
		b, err := strconv.ParseBool(s)
		if err != nil {
			return err
		}
		v.SetBool(b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		// Parse at the field's own width so an out-of-range value errors rather
		// than silently wrapping (SetInt truncates without complaint).
		n, err := strconv.ParseInt(s, 10, v.Type().Bits())
		if err != nil {
			return err
		}
		v.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(s, 10, v.Type().Bits())
		if err != nil {
			return err
		}
		v.SetUint(n)
	case reflect.Float32, reflect.Float64:
		fl, err := strconv.ParseFloat(s, v.Type().Bits())
		if err != nil {
			return err
		}
		v.SetFloat(fl)
	default:
		if v.Type() == reflect.TypeOf(time.Time{}) {
			ts, err := time.Parse(time.RFC3339, s)
			if err != nil {
				return err
			}
			v.Set(reflect.ValueOf(ts))
			return nil
		}
		return fmt.Errorf("unsupported bind type %s", v.Type())
	}
	return nil
}

// wireName resolves a field's query-parameter name: q tag, then col, then snake.
func wireName(f reflect.StructField) string {
	if q := f.Tag.Get("q"); q != "" {
		return q
	}
	if c := f.Tag.Get("col"); c != "" {
		return c
	}
	if d := f.Tag.Get("db"); d != "" {
		return strings.Split(d, ",")[0]
	}
	return snake(f.Name)
}

// jsonName returns a holder sub-field's operator word from its json tag.
func jsonName(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	if tag == "" {
		return strings.ToLower(f.Name)
	}
	return strings.Split(tag, ",")[0]
}

func first(values url.Values, key string) (string, bool) {
	vs, ok := values[key]
	if !ok || len(vs) == 0 {
		return "", false
	}
	return vs[0], true
}

// listValues returns all values for key, splitting any comma-separated entries
// so role=a,b and role=a&role=b are equivalent.
func listValues(values url.Values, key string) []string {
	vs, ok := values[key]
	if !ok {
		return nil
	}
	var out []string
	for _, v := range vs {
		for _, part := range strings.Split(v, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}
