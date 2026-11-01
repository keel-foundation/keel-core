package testutil

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/keel-foundation/keel-core/internal/db"
)

func Open(t *testing.T) *sql.DB {
	t.Helper()

	dsn := os.Getenv("TUBEKEEL_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TUBEKEEL_TEST_DATABASE_URL to run database tests")
	}

	ctx := context.Background()
	conn, err := db.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	if err := db.Migrate(ctx, conn); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `TRUNCATE videos, channels, actors RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate test database: %v", err)
	}
	return conn
}
