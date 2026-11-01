package db_test

import (
	"context"
	"testing"

	"github.com/keel-foundation/keel-core/internal/db"
	"github.com/keel-foundation/keel-core/internal/testutil"
)

func TestMigrateCreatesSchemaAndIsIdempotent(t *testing.T) {
	conn := testutil.Open(t)
	ctx := context.Background()

	var count int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if count == 0 {
		t.Fatal("expected at least one applied migration")
	}

	for _, table := range []string{"actors", "channels", "videos"} {
		var exists bool
		if err := conn.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM information_schema.tables
				WHERE table_schema = 'public' AND table_name = $1
			)
		`, table).Scan(&exists); err != nil {
			t.Fatalf("check table %s: %v", table, err)
		}
		if !exists {
			t.Fatalf("table %s is missing", table)
		}
	}

	if err := db.Migrate(ctx, conn); err != nil {
		t.Fatalf("second migration run: %v", err)
	}

	var after int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&after); err != nil {
		t.Fatalf("count migrations after rerun: %v", err)
	}
	if after != count {
		t.Fatalf("migration count changed: %d -> %d", count, after)
	}
}
