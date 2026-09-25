package main

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/spool-reader/spool/internal/db"
	"github.com/spool-reader/spool/internal/feed"
)

func TestRiverFeedJobBatchFallsBackOnUniqueConflict(t *testing.T) {
	url := os.Getenv("SPOOL_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("SPOOL_TEST_DATABASE_URL not set")
	}
	database, err := db.Open(url)
	if err != nil {
		t.Fatalf("Open database: %v", err)
	}
	defer database.Close()
	ctx := context.Background()
	if err := db.Migrate(ctx, database); err != nil {
		t.Fatalf("Migrate database: %v", err)
	}
	client, err := newRiverInsertClient(database, nil)
	if err != nil {
		t.Fatalf("create River client: %v", err)
	}
	jobs := &riverFeedJobs{client: client}
	prefix := fmt.Sprintf("integration-%d", time.Now().UnixNano())
	args := []feed.RefreshArgs{
		{FeedID: prefix + "-1", FeedURL: "https://example.com/1"},
		{FeedID: prefix + "-2", FeedURL: "https://example.com/2"},
	}

	if err := jobs.InsertRefreshBatch(ctx, args); err != nil {
		t.Fatalf("first batch insert: %v", err)
	}
	if err := jobs.InsertRefreshBatch(ctx, args); err != nil {
		t.Fatalf("duplicate batch insert: %v", err)
	}
	if err := jobs.InsertRefresh(ctx, args[0]); err != nil {
		t.Fatalf("duplicate single insert: %v", err)
	}

	var count int
	if err := database.QueryRowContext(ctx, `
		SELECT count(*) FROM river_job
		WHERE kind = 'feed.refresh' AND args->>'feed_id' IN ($1, $2)
	`, args[0].FeedID, args[1].FeedID).Scan(&count); err != nil {
		t.Fatalf("count refresh jobs: %v", err)
	}
	if count != len(args) {
		t.Fatalf("refresh job count = %d, want %d", count, len(args))
	}
}
