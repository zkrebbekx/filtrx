package filtrx

import "errors"

// Sentinel errors returned by the listing helpers, wrapped around the underlying
// cause. Match them with errors.Is.
var (
	// ErrCompile indicates a filter struct could not be compiled — an unknown
	// operator, a malformed group tag, or an unsupported field type.
	ErrCompile = errors.New("filtrx: compile error")
	// ErrQuery indicates the database rejected or failed the generated query.
	ErrQuery = errors.New("filtrx: query error")
)
