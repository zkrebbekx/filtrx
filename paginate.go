package filtrx

// PageInfo describes the result of a paginated listing: the window that was
// returned and, when requested, the total matching the filter.
type PageInfo struct {
	// Total is the number of records matching the filter ignoring the page
	// window. It is meaningful only when the request asked for it (IncludeTotal,
	// or any Last request, which must count to resolve its offset).
	Total int
	// Offset is the zero-based index of the first returned record within the
	// full filtered set — the basis for the next/previous cursor.
	Offset int
	// Truncated reports that more records exist past the returned window, i.e.
	// the query was limited and a further row was available. In keyset mode
	// (Seek) it means more rows exist in the paging direction — the basis for
	// hasNextPage (forward) or hasPreviousPage (backward).
	Truncated bool
	// StartCursor and EndCursor are the opaque keyset cursors for the first and
	// last returned rows. They are set only by Seek (keyset) pagination; pass
	// EndCursor as the next page's After, or StartCursor as the previous page's
	// Before. They are empty for offset pagination and for an empty page.
	StartCursor Cursor
	EndCursor   Cursor
}

// PagingParams captures pagination arguments in the Relay style, but over record
// offsets rather than opaque cursors. Before and After are zero-based offsets;
// First and Last bound the page size from the start or end of the (optionally
// Before/After constrained) range. First and Last are mutually exclusive.
type PagingParams struct {
	Before       *int
	After        *int
	First        *int
	Last         *int
	IncludeTotal bool
}

// TruncateAt returns paging params that take the first limit records. A limit of
// zero or less returns the zero value, which selects everything.
func TruncateAt(limit int) (p PagingParams) {
	if limit > 0 {
		p.First = &limit
	}
	return p
}

// Paginate resolves the paging arguments into a function that computes the SQL
// limit and offset, together with a flag indicating whether the total record
// count must be obtained before that function is called.
//
// The returned paginator is nil when no records should be retrieved at all (for
// example First is zero, or a Before/After range is empty). In that case the
// needsTotal flag still reflects whether the caller asked for the count, so an
// empty page can report an accurate Total.
//
// needsTotal is true for a non-nil paginator only when paging from the end
// (Last): resolving an offset measured from the end requires the count up front.
// Every other case obtains the total in the same query via COUNT(*) OVER(), so
// no separate pre-count is paid — that "fast total" is always on.
//
// Paginate panics on contradictory arguments (First with Last, or a negative
// First/Last); these are programming errors, not runtime input.
func Paginate(p PagingParams) (paginator func(total int) (limit int, offset int), needsTotal bool) {
	var limit, offset int
	if p.First != nil && p.Last != nil {
		panic("filtrx: cannot specify both First and Last")
	}
	if p.First != nil {
		if *p.First < 0 {
			panic("filtrx: First must be non-negative")
		}
		if *p.First == 0 {
			return nil, p.IncludeTotal
		}
		limit = *p.First
	}
	if p.Last != nil {
		if *p.Last < 0 {
			panic("filtrx: Last must be non-negative")
		}
		if *p.Last == 0 {
			return nil, p.IncludeTotal
		}
		limit = -*p.Last
	}
	if p.After != nil && *p.After >= 0 {
		offset = *p.After + 1
	}
	if p.Before != nil {
		if *p.Before <= offset {
			return nil, p.IncludeTotal
		}
		if limit < 0 {
			limit = -limit
			if offset == 0 && *p.Before > limit {
				offset = *p.Before - limit
			}
		}
		if limit == 0 || *p.Before-offset < limit {
			limit = *p.Before - offset
		}
	}
	return func(total int) (int, int) {
		if limit < 0 {
			if total+limit > offset {
				return -limit, total + limit
			}
			return -limit, offset
		}
		return limit, offset
	}, limit < 0
}
