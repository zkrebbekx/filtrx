package filtrx

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jmoiron/sqlx"
)

// singleTable rejects a mutation whose source carries an alias or joins (as a
// For-filter source does). DELETE/UPDATE against an aliased or joined source is
// invalid or dialect-specific SQL; mutations operate on one plain table.
func (q *Query) singleTable() error {
	if q.from != nil && (q.from.alias != "" || len(q.from.joins) > 0) {
		return fmt.Errorf("%w: Delete/Update operate on a single table, not a joined or aliased source", ErrCompile)
	}
	return nil
}

// Unfiltered authorises a Delete or Update with no WHERE clause, i.e. one that
// affects every row. Without it those calls refuse an empty filter, so a filter
// struct that happens to be all-unset can never silently wipe a table. It has no
// effect on List.
func (q *Query) Unfiltered() *Query {
	q.unfiltered = true
	return q
}

// Delete runs DELETE FROM the query's table using the compiled filter as its
// WHERE, returning the number of rows affected. The filter comes from Where/Cond
// exactly as for List; ordering, paging and projection are ignored.
//
// As a safeguard Delete refuses to run without a filter (which would delete every
// row); call Unfiltered to authorise a whole-table delete deliberately.
func (q *Query) Delete(ctx context.Context, db sqlx.ExecerContext) (int64, error) {
	if q.err != nil {
		return 0, fmt.Errorf("%w: %w", ErrCompile, q.err)
	}
	if err := q.singleTable(); err != nil {
		return 0, err
	}
	where, args := Build(q.effectiveCond(), q.dialect)
	if where == "" && !q.unfiltered {
		return 0, fmt.Errorf("%w: Delete has no filter; call Unfiltered to delete every row", ErrCompile)
	}

	var sb strings.Builder
	sb.WriteString("DELETE FROM ")
	sb.WriteString(q.fromClause())
	if where != "" {
		sb.WriteString(" WHERE ")
		sb.WriteString(where)
	}
	return exec(ctx, db, sb.String(), args)
}

// Update runs UPDATE on the query's table, applying set as the assignments and
// the compiled filter as the WHERE, returning the number of rows affected. The
// set columns are keys, quoted as identifiers; their values bind as parameters,
// and the WHERE parameters follow them. Assignments are emitted in sorted column
// order so the statement is deterministic.
//
// Like Delete, Update refuses to run without a filter unless Unfiltered was
// called. An empty set is an error.
func (q *Query) Update(ctx context.Context, db sqlx.ExecerContext, set map[string]any) (int64, error) {
	if q.err != nil {
		return 0, fmt.Errorf("%w: %w", ErrCompile, q.err)
	}
	if err := q.singleTable(); err != nil {
		return 0, err
	}
	if len(set) == 0 {
		return 0, fmt.Errorf("%w: Update needs at least one column to set", ErrCompile)
	}
	cond := q.effectiveCond()
	where, whereArgs := Build(cond, q.dialect)
	if where == "" && !q.unfiltered {
		return 0, fmt.Errorf("%w: Update has no filter; call Unfiltered to update every row", ErrCompile)
	}

	cols := make([]string, 0, len(set))
	for c := range set {
		cols = append(cols, c)
	}
	sort.Strings(cols)

	var sb strings.Builder
	sb.WriteString("UPDATE ")
	sb.WriteString(q.fromClause())
	sb.WriteString(" SET ")
	args := make([]any, 0, len(cols)+len(whereArgs))
	for i, c := range cols {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(q.dialect.quoteIdent(c))
		sb.WriteString(" = ")
		sb.WriteString(q.dialect.placeholder(i + 1))
		args = append(args, set[c])
	}
	if where != "" {
		// Re-render the WHERE so its placeholders follow the SET assignments.
		where, whereArgs = buildAt(cond, q.dialect, len(cols))
		sb.WriteString(" WHERE ")
		sb.WriteString(where)
	}
	args = append(args, whereArgs...)
	return exec(ctx, db, sb.String(), args)
}

// exec runs a statement and returns the affected-row count, wrapping failures in
// ErrQuery. A driver that does not report RowsAffected yields 0 without error.
func exec(ctx context.Context, db sqlx.ExecerContext, query string, args []any) (int64, error) {
	res, err := db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrQuery, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, nil
	}
	return n, nil
}
