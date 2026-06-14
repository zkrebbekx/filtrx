package filtrx

import (
	"bytes"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"reflect"
)

// Opt is an optional value used in filter structs. Its zero value is unset,
// which the compiler skips when building the WHERE clause. Unlike a pointer it
// never allocates, can be compared by value, and — crucially — can carry a
// genuine zero value (0, "", false) as a real filter rather than being confused
// with "absent".
//
// Set one with Some; leave it as the zero value (or use None) for unset.
// Decoding JSON sets it automatically: a present key becomes set, a missing key
// stays unset, so request bodies map straight onto filter structs.
type Opt[T any] struct {
	val T
	set bool
}

// Some returns a set Opt carrying v.
func Some[T any](v T) Opt[T] { return Opt[T]{val: v, set: true} }

// None returns an unset Opt. It is identical to the zero value and exists only
// to read clearly at a call site.
func None[T any]() Opt[T] { return Opt[T]{} }

// Get returns the value and whether it is set.
func (o Opt[T]) Get() (T, bool) { return o.val, o.set }

// IsSet reports whether the Opt carries a value.
func (o Opt[T]) IsSet() bool { return o.set }

// Or returns the value if set, otherwise def.
func (o Opt[T]) Or(def T) T {
	if o.set {
		return o.val
	}
	return def
}

// value implements the internal setter interface read by the filter compiler.
func (o Opt[T]) value() any { return o.val }

// isSet implements the internal setter interface read by the filter compiler.
func (o Opt[T]) isSet() bool { return o.set }

// MarshalJSON encodes the contained value, or null when unset.
func (o Opt[T]) MarshalJSON() ([]byte, error) {
	if !o.set {
		return []byte("null"), nil
	}
	return json.Marshal(o.val)
}

// UnmarshalJSON decodes into the contained value and marks the Opt set. A JSON
// null clears it. A missing object key never calls this method, so the Opt
// stays unset — giving exact presence detection from request payloads.
func (o *Opt[T]) UnmarshalJSON(b []byte) error {
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		o.set = false
		return nil
	}
	if err := json.Unmarshal(b, &o.val); err != nil {
		return err
	}
	o.set = true
	return nil
}

// setter is the value-access contract the filter compiler uses to read scalar
// optional fields without reflecting into Opt's unexported fields.
type setter interface {
	isSet() bool
	value() any
}

// setString parses a query-string value into the contained value and marks the
// Opt set, implementing stringSetter for Bind. Parsing follows the concrete
// type T (primitives plus time.Time).
func (o *Opt[T]) setString(s string) error {
	if err := parseScalar(reflect.ValueOf(&o.val).Elem(), s); err != nil {
		return err
	}
	o.set = true
	return nil
}

// Scan implements sql.Scanner so an Opt may also receive a value from a row,
// not only feed one into a query. A NULL leaves the Opt unset.
func (o *Opt[T]) Scan(src any) error {
	if src == nil {
		o.set = false
		return nil
	}
	v, ok := src.(T)
	if !ok {
		return fmt.Errorf("filtrx: cannot scan %T into Opt[%T]", src, o.val)
	}
	o.val, o.set = v, true
	return nil
}

// Value implements driver.Valuer so a set Opt can be passed directly as a query
// argument; an unset Opt yields SQL NULL.
func (o Opt[T]) Value() (driver.Value, error) {
	if !o.set {
		return nil, nil
	}
	return o.val, nil
}
