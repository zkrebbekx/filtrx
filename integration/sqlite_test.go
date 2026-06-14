//go:build integration

package integration

import (
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/zkrebbekx/filtrx"
	_ "modernc.org/sqlite"
)

func TestSQLite(t *testing.T) {
	db, err := sqlx.Connect("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	// An in-memory SQLite database is private to a single connection; pin the
	// pool to one so the schema persists across the suite's queries.
	db.SetMaxOpenConns(1)

	runSuite(t, db, filtrx.SQLite)
}
