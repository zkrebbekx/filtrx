package filtrx

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
)

// Edge is one element of a Relay connection: the record and its opaque cursor.
type Edge[T any] struct {
	Node   T      `json:"node"`
	Cursor Cursor `json:"cursor"`
}

// RelayPageInfo is the GraphQL Relay PageInfo object. Unlike filtrx's offset
// PageInfo it carries the four Relay fields directly, so a resolver can return it
// without translation.
type RelayPageInfo struct {
	HasNextPage     bool   `json:"hasNextPage"`
	HasPreviousPage bool   `json:"hasPreviousPage"`
	StartCursor     Cursor `json:"startCursor"`
	EndCursor       Cursor `json:"endCursor"`
}

// Connection is a Relay-style connection: the edges with per-row cursors, the
// page info, and the total when it was requested (TotalCount is -1 otherwise).
// It is the shape a GraphQL connection resolver returns.
type Connection[T any] struct {
	Edges      []Edge[T]     `json:"edges"`
	PageInfo   RelayPageInfo `json:"pageInfo"`
	TotalCount int           `json:"totalCount"`
}

// ListConnection runs a keyset (Seek) query and assembles a Relay connection:
// one edge per row with its cursor, plus hasNextPage / hasPreviousPage. The query
// must be in Seek mode (see Query.Seek); it errors as a compile error otherwise.
//
// hasNextPage and hasPreviousPage follow the Relay convention for keyset paging:
// the truncation flag drives the next/previous edge in the paging direction, and
// having paged from a cursor implies a page exists in the opposite direction.
func ListConnection[T any](ctx context.Context, db sqlx.QueryerContext, q *Query) (Connection[T], error) {
	if q.err != nil {
		return Connection[T]{}, fmt.Errorf("%w: %w", ErrCompile, q.err)
	}
	if q.seek == nil {
		return Connection[T]{}, fmt.Errorf("%w: ListConnection requires a Seek query", ErrCompile)
	}

	var rows []T
	res, err := runKeyset(ctx, db, q, &rows)
	if err != nil {
		return Connection[T]{}, err
	}

	conn := Connection[T]{
		Edges:      make([]Edge[T], len(rows)),
		TotalCount: -1,
	}
	for i := range rows {
		conn.Edges[i] = Edge[T]{Node: rows[i], Cursor: res.cursors[i]}
	}
	if n := len(res.cursors); n > 0 {
		conn.PageInfo.StartCursor = res.cursors[0]
		conn.PageInfo.EndCursor = res.cursors[n-1]
	}
	// Paging forward: truncation means another page follows; a cursor means one
	// precedes. Paging backward, the two swap.
	pagedFromCursor := q.seek.cursor != ""
	if q.seek.before {
		conn.PageInfo.HasPreviousPage = res.truncated
		conn.PageInfo.HasNextPage = pagedFromCursor
	} else {
		conn.PageInfo.HasNextPage = res.truncated
		conn.PageInfo.HasPreviousPage = pagedFromCursor
	}
	if res.totalSet {
		conn.TotalCount = res.total
	}
	return conn, nil
}
