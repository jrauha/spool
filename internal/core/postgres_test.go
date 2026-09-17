package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

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
		IconURL:     "https://example.com/favicon.ico",
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
	if found.URL != created.URL || found.Title != created.Title || found.IconURL != created.IconURL {
		t.Fatalf("feed = %#v, want %#v", found, created)
	}

	refreshedAt := time.Now().UTC()
	created.Title = "Updated"
	created.RefreshedAt = &refreshedAt
	updated, err := store.UpdateFeed(ctx, created)
	if err != nil {
		t.Fatalf("UpdateFeed returned error: %v", err)
	}
	if updated.Title != "Updated" || updated.RefreshedAt == nil {
		t.Fatalf("feed = %#v, want updated feed", updated)
	}

	feeds, err := store.ListFeeds(ctx)
	if err != nil {
		t.Fatalf("ListFeeds returned error: %v", err)
	}
	if len(feeds) != 1 || feeds[0].ID != created.ID {
		t.Fatalf("feeds = %#v, want created feed", feeds)
	}
}

func TestPostgresStoreItems(t *testing.T) {
	database := openTestDB(t)
	store := NewPostgresStore(database)
	ctx := context.Background()
	feed := testFeed(t, store, ctx)
	publishedAt := time.Now().UTC().Add(-time.Hour)

	item, created, err := store.UpsertItem(ctx, Item{
		FeedID:      feed.ID,
		GUID:        "item-1",
		URL:         "https://example.com/items/1",
		Title:       "First item",
		Summary:     "Original summary",
		PublishedAt: &publishedAt,
	})
	if err != nil {
		t.Fatalf("UpsertItem returned error: %v", err)
	}
	if !created {
		t.Fatal("item was not created")
	}

	item, created, err = store.UpsertItem(ctx, Item{
		FeedID:  feed.ID,
		GUID:    "item-1",
		URL:     "https://example.com/items/1",
		Title:   "First item",
		Summary: "Updated summary",
	})
	if err != nil {
		t.Fatalf("UpsertItem update returned error: %v", err)
	}
	if created {
		t.Fatal("existing item was reported as created")
	}
	if item.Summary != "Updated summary" {
		t.Fatalf("summary = %q, want updated summary", item.Summary)
	}

	items, err := store.ListItems(ctx, feed.ID, 10, 0)
	if err != nil {
		t.Fatalf("ListItems returned error: %v", err)
	}
	if len(items) != 1 || items[0].ID != item.ID {
		t.Fatalf("items = %#v, want updated item", items)
	}

	latest, err := store.ListLatestItems(ctx, 1, 0)
	if err != nil {
		t.Fatalf("ListLatestItems returned error: %v", err)
	}
	if len(latest) != 1 || latest[0].ID != item.ID {
		t.Fatalf("latest = %#v, want updated item", latest)
	}

	newerPublishedAt := publishedAt.Add(time.Hour)
	_, _, err = store.UpsertItem(ctx, Item{
		FeedID:      feed.ID,
		GUID:        "item-2",
		URL:         "https://example.com/items/2",
		Title:       "Second item",
		PublishedAt: &newerPublishedAt,
	})
	if err != nil {
		t.Fatalf("UpsertItem newer returned error: %v", err)
	}

	latest, err = store.ListLatestItems(ctx, 1, 1)
	if err != nil {
		t.Fatalf("ListLatestItems page returned error: %v", err)
	}
	if len(latest) != 1 || latest[0].ID != item.ID {
		t.Fatalf("latest page = %#v, want first item", latest)
	}
}

func TestPostgresStoreSubscriptionReadState(t *testing.T) {
	database := openTestDB(t)
	store := NewPostgresStore(database)
	ctx := context.Background()
	feed := testFeed(t, store, ctx)
	userID := testUserID(t, database, ctx)
	if err := store.CreateSubscription(ctx, userID, feed.ID); err != nil {
		t.Fatalf("CreateSubscription returned error: %v", err)
	}
	item, _, err := store.UpsertItem(ctx, Item{FeedID: feed.ID, GUID: "item-read", Title: "Read item"})
	if err != nil {
		t.Fatalf("UpsertItem returned error: %v", err)
	}

	feeds, err := store.ListFeedsForUser(ctx, userID)
	if err != nil {
		t.Fatalf("ListFeedsForUser returned error: %v", err)
	}
	if len(feeds) != 1 || feeds[0].UnreadCount != 1 {
		t.Fatalf("feeds = %#v, want unread count", feeds)
	}

	if err := store.MarkItemRead(ctx, userID, item.ID); err != nil {
		t.Fatalf("MarkItemRead returned error: %v", err)
	}
	feeds, err = store.ListFeedsForUser(ctx, userID)
	if err != nil {
		t.Fatalf("ListFeedsForUser returned error: %v", err)
	}
	if len(feeds) != 1 || feeds[0].UnreadCount != 0 {
		t.Fatalf("feeds = %#v, want no unread items", feeds)
	}
	items, err := store.ListLatestItemsForUser(ctx, userID, 10, 0)
	if err != nil {
		t.Fatalf("ListLatestItemsForUser returned error: %v", err)
	}
	if len(items) != 1 || items[0].ReadAt == nil {
		t.Fatalf("items = %#v, want read state", items)
	}

	if err := store.MarkItemUnread(ctx, userID, item.ID); err != nil {
		t.Fatalf("MarkItemUnread returned error: %v", err)
	}
	items, err = store.ListItemsForUser(ctx, userID, feed.ID, 10, 0)
	if err != nil {
		t.Fatalf("ListItemsForUser returned error: %v", err)
	}
	if len(items) != 1 || items[0].ReadAt != nil {
		t.Fatalf("items = %#v, want unread state", items)
	}

	if err := store.MarkFeedRead(ctx, userID, feed.ID); err != nil {
		t.Fatalf("MarkFeedRead returned error: %v", err)
	}
	items, err = store.ListItemsForUser(ctx, userID, feed.ID, 10, 0)
	if err != nil {
		t.Fatalf("ListItemsForUser after feed read returned error: %v", err)
	}
	if len(items) != 1 || items[0].ReadAt == nil {
		t.Fatalf("items = %#v, want read before state", items)
	}

	if err := store.MarkItemUnread(ctx, userID, item.ID); err != nil {
		t.Fatalf("MarkItemUnread after feed read returned error: %v", err)
	}
	if err := store.MarkAllRead(ctx, userID); err != nil {
		t.Fatalf("MarkAllRead returned error: %v", err)
	}
	items, err = store.ListItemsForUser(ctx, userID, feed.ID, 10, 0)
	if err != nil {
		t.Fatalf("ListItemsForUser after all read returned error: %v", err)
	}
	if len(items) != 1 || items[0].ReadAt == nil {
		t.Fatalf("items = %#v, want all read state", items)
	}
}

func TestPostgresStoreEnqueueDueRefresh(t *testing.T) {
	database := openTestDB(t)
	store := NewPostgresStore(database)
	ctx := context.Background()
	feed := testFeed(t, store, ctx)
	userID := testUserID(t, database, ctx)
	if err := store.CreateSubscription(ctx, userID, feed.ID); err != nil {
		t.Fatalf("CreateSubscription returned error: %v", err)
	}

	queued, err := store.EnqueueDueRefresh(ctx, time.Hour)
	if err != nil {
		t.Fatalf("EnqueueDueRefresh returned error: %v", err)
	}
	if queued != 1 {
		t.Fatalf("queued = %d, want 1", queued)
	}

	job, err := store.ClaimRefresh(ctx, time.Minute)
	if err != nil {
		t.Fatalf("ClaimRefresh returned error: %v", err)
	}
	if job.FeedID != feed.ID {
		t.Fatalf("feed ID = %q, want %q", job.FeedID, feed.ID)
	}
}

func TestPostgresStoreRefreshJobs(t *testing.T) {
	database := openTestDB(t)
	store := NewPostgresStore(database)
	ctx := context.Background()
	feed := testFeed(t, store, ctx)

	if err := store.EnqueueRefresh(ctx, feed.ID, time.Now().UTC()); err != nil {
		t.Fatalf("EnqueueRefresh returned error: %v", err)
	}
	job, err := store.ClaimRefresh(ctx, time.Minute)
	if err != nil {
		t.Fatalf("ClaimRefresh returned error: %v", err)
	}
	if job.FeedID != feed.ID || job.LeaseToken == "" || job.Attempts != 1 {
		t.Fatalf("job = %#v", job)
	}

	_, err = store.ClaimRefresh(ctx, time.Minute)
	if !errors.Is(err, ErrNoRefreshJob) {
		t.Fatalf("ClaimRefresh error = %v, want %v", err, ErrNoRefreshJob)
	}

	retried, err := store.RetryRefresh(ctx, job.FeedID, job.LeaseToken, time.Now().UTC())
	if err != nil {
		t.Fatalf("RetryRefresh returned error: %v", err)
	}
	if !retried {
		t.Fatal("job was not retried")
	}

	job, err = store.ClaimRefresh(ctx, time.Minute)
	if err != nil {
		t.Fatalf("ClaimRefresh retry returned error: %v", err)
	}
	if job.Attempts != 2 {
		t.Fatalf("attempts = %d, want 2", job.Attempts)
	}

	completed, err := store.CompleteRefresh(ctx, job.FeedID, job.LeaseToken)
	if err != nil {
		t.Fatalf("CompleteRefresh returned error: %v", err)
	}
	if !completed {
		t.Fatal("job was not completed")
	}
}

func TestPostgresStoreEvents(t *testing.T) {
	database := openTestDB(t)
	store := NewPostgresStore(database)
	ctx := context.Background()

	event, err := store.AppendEvent(ctx, Event{
		Name:    EventFeedAdded,
		Entity:  "feed",
		Payload: json.RawMessage(`{"url":"https://example.com/feed.xml"}`),
	})
	if err != nil {
		t.Fatalf("AppendEvent returned error: %v", err)
	}
	if event.ID == "" {
		t.Fatal("event ID is empty")
	}
	var payload map[string]string
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatalf("Unmarshal payload: %v", err)
	}
	if payload["url"] != "https://example.com/feed.xml" {
		t.Fatalf("payload = %s", event.Payload)
	}
}

func testUserID(t *testing.T, database *sql.DB, ctx context.Context) string {
	t.Helper()

	var id string
	err := database.QueryRowContext(ctx, `
		INSERT INTO users (email, password_hash) VALUES ($1, $2) RETURNING id::text
	`, "reader@example.com", "hash").Scan(&id)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return id
}

func testFeed(t *testing.T, store *PostgresStore, ctx context.Context) Feed {
	t.Helper()

	feed, err := store.CreateFeed(ctx, Feed{
		URL:   "https://example.com/feed.xml",
		Title: "Example",
	})
	if err != nil {
		t.Fatalf("CreateFeed returned error: %v", err)
	}
	return feed
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
