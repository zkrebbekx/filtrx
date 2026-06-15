//go:build integration

package integration

import (
	"context"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/testcontainers/testcontainers-go/modules/mysql"
	"github.com/zkrebbekx/filtrx"
)

func TestMySQL(t *testing.T) {
	ctx := context.Background()

	container, err := mysql.Run(ctx, "mysql:8.0",
		mysql.WithDatabase("filtrx"),
		mysql.WithUsername("filtrx"),
		mysql.WithPassword("filtrx"),
	)
	if err != nil {
		t.Skipf("skipping MySQL integration (Docker unavailable?): %v", err)
	}
	defer func() { _ = container.Terminate(ctx) }()

	dsn, err := container.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	db, err := sqlx.Connect("mysql", dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = db.Close() }()

	runSuite(t, db, filtrx.MySQL)
	runMutations(t, db, filtrx.MySQL)
}
