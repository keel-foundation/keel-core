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

	dsn := os.Getenv("KEELTUBE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set KEELTUBE_TEST_DATABASE_URL to run database tests")
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
	if _, err := conn.ExecContext(ctx, `
		TRUNCATE media_scans, media_jobs, media_assets, video_subtitles, video_versions,
		         media_uploads, media_blocklist, comment_likes, comments, watch_history,
		         saved_videos, video_reactions, subscriptions, actor_embeddings,
		         video_embeddings, auth_events, password_resets, email_verifications,
		         sessions, accounts, videos, channels, actors
		RESTART IDENTITY CASCADE
	`); err != nil {
		t.Fatalf("truncate test database: %v", err)
	}
	return conn
}
