// Command examples is a small HTTP server that demonstrates every filtrx feature
// against a real PostgreSQL database: struct filters from the wire, offset and
// keyset pagination, Relay connections, joins, EXISTS, grouping, full-text search
// with relevance, array operators, soft deletes and filter-driven CRUD.
//
// Run it with docker compose (see README), or point DATABASE_URL at any Postgres
// and `go run .`. On start it applies schema.sql (drops and recreates the demo
// tables) and serves on :8080.
package main

import (
	_ "embed"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
)

//go:embed schema.sql
var schema string

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://filtrx:filtrx@localhost:5432/filtrx?sslmode=disable"
	}

	db := mustConnect(dsn)
	defer func() { _ = db.Close() }()

	if _, err := db.Exec(schema); err != nil {
		log.Fatalf("apply schema: %v", err)
	}
	log.Println("schema applied and seeded")

	srv := &Server{store: &Store{db: db}}
	addr := ":8080"
	log.Printf("filtrx examples listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, srv.routes()))
}

// mustConnect retries briefly so the server can start alongside Postgres under
// docker compose, where the database may not be ready on the first try.
func mustConnect(dsn string) *sqlx.DB {
	var lastErr error
	for i := 0; i < 30; i++ {
		db, err := sqlx.Connect("postgres", dsn)
		if err == nil {
			return db
		}
		lastErr = err
		log.Printf("waiting for database (%d/30): %v", i+1, err)
		time.Sleep(time.Second)
	}
	log.Fatalf("connect: %v", lastErr)
	return nil
}
