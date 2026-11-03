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

	for _, table := range []string{
		"actors", "channels", "videos",
		"accounts", "sessions", "email_verifications", "password_resets", "auth_events",
		"subscriptions", "video_reactions", "saved_videos", "watch_history",
		"comments", "comment_likes", "video_embeddings", "actor_embeddings",
		"video_versions", "video_subtitles", "media_uploads", "media_jobs",
		"media_scans", "media_blocklist", "media_assets",
		"video_view_sessions", "video_view_events", "discovery_events",
		"video_daily_analytics", "notifications",
	} {
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

	for _, column := range []struct {
		table string
		name  string
	}{
		{"channels", "subscribers"},
		{"channels", "verified"},
		{"videos", "public_id"},
		{"videos", "format"},
		{"videos", "category"},
		{"videos", "tags"},
		{"videos", "views"},
		{"videos", "likes"},
		{"videos", "comments_count"},
		{"notifications", "actor_id"},
		{"notifications", "dedupe_key"},
		{"notifications", "read_at"},
	} {
		var exists bool
		if err := conn.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2
			)
		`, column.table, column.name).Scan(&exists); err != nil {
			t.Fatalf("check column %s.%s: %v", column.table, column.name, err)
		}
		if !exists {
			t.Fatalf("column %s.%s is missing", column.table, column.name)
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
