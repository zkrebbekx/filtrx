package filtrx

import (
	"fmt"
	"reflect"
	"strings"
)

// Table is a marker field type declaring the base table of a join-backed filter
// struct. Add one field of this type with table and (optionally) as tags:
//
//	Base filtrx.Table `table:"users" as:"u"`
//
// The field name is yours to choose; only the type and tags matter.
type Table struct{}

// Join is a marker field type declaring one joined table. Add one field per join
// with table, on, and optional as and type tags:
//
//	Org filtrx.Join `table:"organizations" as:"o" on:"o.id = u.org_id"`
//	Pay filtrx.Join `table:"payments" as:"p" on:"p.order_id = o.id" type:"left"`
//
// type defaults to an inner join; accepted values are inner, left, right and
// full. The on expression is emitted verbatim, so it must not be built from
// request data.
type Join struct{}

var (
	tableType = reflect.TypeOf(Table{})
	joinType  = reflect.TypeOf(Join{})
)

// sourceSpec is the FROM clause and joins compiled from a filter struct's marker
// fields. It is nil for a single-table filter that declares no Table field.
type sourceSpec struct {
	table string
	alias string
	joins []joinSpec
}

type joinSpec struct {
	kind  string // "", "LEFT", "RIGHT", "FULL" (empty = inner)
	table string
	alias string
	on    string
}

// parseTable reads a Table marker field's tags into the source's base table.
func parseTable(f reflect.StructField, s *sourceSpec) error {
	table := f.Tag.Get("table")
	if table == "" {
		return fmt.Errorf("filtrx: Table field %s needs a table tag", f.Name)
	}
	if s.table != "" {
		return fmt.Errorf("filtrx: filter has more than one Table field")
	}
	s.table = table
	s.alias = f.Tag.Get("as")
	return nil
}

// parseJoin reads a Join marker field's tags into a joinSpec.
func parseJoin(f reflect.StructField) (joinSpec, error) {
	table := f.Tag.Get("table")
	if table == "" {
		return joinSpec{}, fmt.Errorf("filtrx: Join field %s needs a table tag", f.Name)
	}
	on := f.Tag.Get("on")
	if on == "" {
		return joinSpec{}, fmt.Errorf("filtrx: Join field %s needs an on tag", f.Name)
	}
	kind, err := joinKind(f.Tag.Get("type"), f.Name)
	if err != nil {
		return joinSpec{}, err
	}
	return joinSpec{kind: kind, table: table, alias: f.Tag.Get("as"), on: on}, nil
}

func joinKind(tag, field string) (string, error) {
	switch strings.ToLower(tag) {
	case "", "inner":
		return "", nil
	case "left":
		return "LEFT", nil
	case "right":
		return "RIGHT", nil
	case "full":
		return "FULL", nil
	default:
		return "", fmt.Errorf("filtrx: Join field %s has unknown type %q", field, tag)
	}
}

// render writes the FROM clause and joins, quoting identifiers with d. Join on
// expressions are emitted verbatim (they are developer-authored, not request
// input).
func (s *sourceSpec) render(d Dialect) string {
	var sb strings.Builder
	sb.WriteString(d.quoteIdent(s.table))
	if s.alias != "" {
		sb.WriteByte(' ')
		sb.WriteString(d.quoteIdent(s.alias))
	}
	for _, j := range s.joins {
		sb.WriteByte(' ')
		if j.kind != "" {
			sb.WriteString(j.kind)
			sb.WriteByte(' ')
		}
		sb.WriteString("JOIN ")
		sb.WriteString(d.quoteIdent(j.table))
		if j.alias != "" {
			sb.WriteByte(' ')
			sb.WriteString(d.quoteIdent(j.alias))
		}
		sb.WriteString(" ON ")
		sb.WriteString(j.on)
	}
	return sb.String()
}
