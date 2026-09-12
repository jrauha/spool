package core

import (
	"context"
	"database/sql"
	"os"
	"testing"

	dbkit "github.com/spool-reader/spool/internal/db"
)

const testDBEnv = "SPOOL_TEST_DATABASE_URL"

func TestPostgresStoreFeeds(t *testing.T) {
	database := openTestDB(t)
	store := NewPostgresStore(database)
	ctx := context.Background()

	created, err := store.CreateFeed(ctx, Feed{
		URL:         "https://example.com/feed.xml",
		Title:       "Example",
		Description: "Example feed",
		SiteURL:     "https://example.com",
	})
	if err != nil {
		t.Fatalf("CreateFeed returned error: %v", err)
	}
	if created.ID == "" {
		t.Fatal("feed ID is empty")
	}

	found, err := store.FindFeed(ctx, created.ID)
	if err != nil {
		t.Fatalf("FindFeed returned error: %v", err)
	}
	if found.URL != created.URL || found.Title != created.Title {
		t.Fatalf("feed = %#v, want %#v", found, created)
	}

	feeds, err := store.ListFeeds(ctx)
	if err != nil {
		t.Fatalf("ListFeeds returned error: %v", err)
	}
	if len(feeds) != 1 || feeds[0].ID != created.ID {
		t.Fatalf("feeds = %#v, want created feed", feeds)
	}
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()

	url := os.Getenv(testDBEnv)
	if url == "" {
		t.Skip(testDBEnv + " not set")
	}

	database, err := dbkit.Open(url)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	ctx := context.Background()
	if err := dbkit.Migrate(ctx, database); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}
	if _, err := database.ExecContext(ctx, `TRUNCATE events, items, feeds, sessions, users RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate tables: %v", err)
	}
	return database
}
