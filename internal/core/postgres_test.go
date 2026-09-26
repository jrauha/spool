package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	dbkit "github.com/spool-reader/spool/internal/db"
	itemquery "github.com/spool-reader/spool/internal/query"
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
		ImageURL:    "https://example.com/items/1/image.png",
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
	asset := Asset{
		ID:        "12345678-1234-1234-1234-123456789abc",
		MediaType: "image/png",
		ByteSize:  128,
		SHA256:    strings.Repeat("a", 64),
	}
	inputs := ItemImageInputs{PageURL: item.URL, ImageURL: item.ImageURL}
	if _, updated, err := store.ReplaceItemAssetIfInputsMatch(ctx, item.ID, inputs, ItemAssetRoleThumbnail, &asset); err != nil || !updated {
		t.Fatalf("ReplaceItemAssetIfInputsMatch returned (%v, %v), want updated", updated, err)
	}
	staleInputs := ItemImageInputs{PageURL: "https://example.com/stale", ImageURL: item.ImageURL}
	if _, updated, err := store.ReplaceItemAssetIfInputsMatch(ctx, item.ID, staleInputs, ItemAssetRoleThumbnail, &Asset{ID: "fedcba98-7654-3210-fedc-ba9876543210"}); err != nil || updated {
		t.Fatalf("ReplaceItemAssetIfInputsMatch accepted stale inputs: updated=%v, err=%v", updated, err)
	}

	item, created, err = store.UpsertItem(ctx, Item{
		FeedID:   feed.ID,
		GUID:     "item-1",
		URL:      "https://example.com/items/1",
		ImageURL: "https://example.com/items/1/image.png",
		Title:    "First item",
		Summary:  "Updated summary",
	})
	if err != nil {
		t.Fatalf("UpsertItem update returned error: %v", err)
	}
	if created {
		t.Fatal("existing item was reported as created")
	}
	if item.Summary != "Updated summary" || item.ImageURL != "https://example.com/items/1/image.png" {
		t.Fatalf("item after upsert = %#v", item)
	}

	found, err := store.FindItem(ctx, item.ID)
	if err != nil {
		t.Fatalf("FindItem returned error: %v", err)
	}
	if found.ID != item.ID || found.Summary != item.Summary || found.ImageURL != item.ImageURL {
		t.Fatalf("found item = %#v, want %#v", found, item)
	}
	userID := testUserID(t, database, ctx)
	if err := store.CreateSubscription(ctx, userID, feed.ID); err != nil {
		t.Fatalf("CreateSubscription returned error: %v", err)
	}
	matches, err := store.QueryItemsForUser(ctx, userID, ItemQuery{Sort: ItemQuerySortNewest, Limit: 10})
	if err != nil {
		t.Fatalf("QueryItemsForUser returned error: %v", err)
	}
	if len(matches) != 1 || len(matches[0].Assets) != 1 || matches[0].Assets[0].AssetID != asset.ID || matches[0].Assets[0].Role != ItemAssetRoleThumbnail {
		t.Fatalf("item assets = %#v", matches)
	}
	foundAsset, err := store.FindAssetForUser(ctx, userID, asset.ID)
	if err != nil || foundAsset.MediaType != "image/png" {
		t.Fatalf("FindAssetForUser = (%#v, %v)", foundAsset, err)
	}
	var otherUserID string
	if err := database.QueryRowContext(ctx, `
		INSERT INTO users (email, password_hash) VALUES ($1, $2) RETURNING id::text
	`, "other-reader@example.com", "hash").Scan(&otherUserID); err != nil {
		t.Fatalf("insert other user: %v", err)
	}
	if _, err := store.FindAssetForUser(ctx, otherUserID, asset.ID); err != ErrAssetNotFound {
		t.Fatalf("FindAssetForUser for unsubscribed user returned %v, want ErrAssetNotFound", err)
	}
	item.ImageURL = "https://example.com/items/1/new-image.png"
	item, _, err = store.UpsertItem(ctx, item)
	if err != nil || item.ImageURL != "https://example.com/items/1/new-image.png" {
		t.Fatalf("image source update = %#v, err=%v", item, err)
	}
	if _, updated, err := store.ReplaceItemAssetIfInputsMatch(ctx, item.ID, inputs, ItemAssetRoleThumbnail, nil); err != nil || updated {
		t.Fatalf("stale attachment removal returned (%v, %v), want no update", updated, err)
	}
	item.URL = ""
	item.ImageURL = ""
	if _, _, err := store.UpsertItem(ctx, item); err != nil {
		t.Fatalf("clear image inputs: %v", err)
	}
	var attachmentCount int
	if err := database.QueryRowContext(ctx, `SELECT count(*) FROM item_assets WHERE item_id = $1`, item.ID).Scan(&attachmentCount); err != nil {
		t.Fatalf("count cleared attachments: %v", err)
	}
	if attachmentCount != 0 {
		t.Fatalf("clearing image inputs left %d attachments", attachmentCount)
	}

	oversized := []struct {
		name string
		item Item
	}{
		{name: "title", item: Item{GUID: "long-title", Title: strings.Repeat("t", MaxItemTitleChars+1)}},
		{name: "author", item: Item{GUID: "long-author", Title: "Item", Author: strings.Repeat("a", MaxItemAuthorChars+1)}},
		{name: "summary", item: Item{GUID: "long-summary", Title: "Item", Summary: strings.Repeat("s", MaxItemSummaryChars+1)}},
		{name: "URL", item: Item{GUID: "long-url", Title: "Item", URL: strings.Repeat("u", MaxItemURLChars+1)}},
		{name: "image URL", item: Item{GUID: "long-image-url", Title: "Item", ImageURL: strings.Repeat("u", MaxItemURLChars+1)}},
	}
	for _, test := range oversized {
		test.item.FeedID = feed.ID
		if _, _, err := store.UpsertItem(ctx, test.item); err == nil {
			t.Errorf("UpsertItem accepted oversized %s", test.name)
		}
	}
}

func TestPostgresStoreUpsertHookIsTransactionalAndTracksImageInputs(t *testing.T) {
	database := openTestDB(t)
	store := NewPostgresStore(database)
	ctx := context.Background()
	feed := testFeed(t, store, ctx)
	item := Item{
		FeedID:   feed.ID,
		GUID:     "transactional-image",
		URL:      "https://example.com/item",
		ImageURL: "https://example.com/image.png",
		Title:    "Item",
	}
	calls := 0
	hook := func(context.Context, *sql.Tx, Item) error {
		calls++
		return nil
	}
	if _, _, err := store.UpsertItemWithHook(ctx, item, hook); err != nil {
		t.Fatalf("first UpsertItemWithHook returned error: %v", err)
	}
	item.Summary = "Updated summary"
	if _, _, err := store.UpsertItemWithHook(ctx, item, hook); err != nil {
		t.Fatalf("summary UpsertItemWithHook returned error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("hook called %d times after summary-only update, want 1", calls)
	}
	item.ImageURL = "https://example.com/new-image.png"
	if _, _, err := store.UpsertItemWithHook(ctx, item, hook); err != nil {
		t.Fatalf("image URL UpsertItemWithHook returned error: %v", err)
	}
	if calls != 2 {
		t.Fatalf("hook called %d times after image URL change, want 2", calls)
	}

	rollbackItem := Item{
		FeedID: feed.ID,
		GUID:   "rollback-item",
		URL:    "https://example.com/rollback",
		Title:  "Rollback",
	}
	hookErr := errors.New("job insert failed")
	if _, _, err := store.UpsertItemWithHook(ctx, rollbackItem, func(context.Context, *sql.Tx, Item) error {
		return hookErr
	}); err != hookErr {
		t.Fatalf("failed enqueue hook returned %v, want %v", err, hookErr)
	}
	var count int
	if err := database.QueryRowContext(ctx, `SELECT count(*) FROM items WHERE feed_id = $1 AND guid = $2`, feed.ID, rollbackItem.GUID).Scan(&count); err != nil {
		t.Fatalf("count rolled back item: %v", err)
	}
	if count != 0 {
		t.Fatalf("failed enqueue left %d item rows, want 0", count)
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
	items, err := store.QueryItemsForUser(ctx, userID, ItemQuery{Sort: ItemQuerySortNewest, Limit: 10})
	if err != nil {
		t.Fatalf("QueryItemsForUser returned error: %v", err)
	}
	if len(items) != 1 || items[0].ReadAt == nil {
		t.Fatalf("items = %#v, want read state", items)
	}

	if err := store.MarkItemUnread(ctx, userID, item.ID); err != nil {
		t.Fatalf("MarkItemUnread returned error: %v", err)
	}
	items, err = store.QueryItemsForUser(ctx, userID, ItemQuery{Sort: ItemQuerySortNewest, Limit: 10})
	if err != nil {
		t.Fatalf("QueryItemsForUser returned error: %v", err)
	}
	if len(items) != 1 || items[0].ReadAt != nil {
		t.Fatalf("items = %#v, want unread state", items)
	}

	if err := store.MarkFeedRead(ctx, userID, feed.ID); err != nil {
		t.Fatalf("MarkFeedRead returned error: %v", err)
	}
	items, err = store.QueryItemsForUser(ctx, userID, ItemQuery{Sort: ItemQuerySortNewest, Limit: 10})
	if err != nil {
		t.Fatalf("QueryItemsForUser after feed read returned error: %v", err)
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
	items, err = store.QueryItemsForUser(ctx, userID, ItemQuery{Sort: ItemQuerySortNewest, Limit: 10})
	if err != nil {
		t.Fatalf("QueryItemsForUser after all read returned error: %v", err)
	}
	if len(items) != 1 || items[0].ReadAt == nil {
		t.Fatalf("items = %#v, want all read state", items)
	}
}

func TestPostgresStoreQueryItemsForUser(t *testing.T) {
	database := openTestDB(t)
	store := NewPostgresStore(database)
	ctx := context.Background()
	feed := testFeed(t, store, ctx)
	otherFeed, err := store.CreateFeed(ctx, Feed{URL: "https://other.example/feed.xml", Title: "Other"})
	if err != nil {
		t.Fatalf("CreateFeed returned error: %v", err)
	}
	userID := testUserID(t, database, ctx)
	if err := store.CreateSubscription(ctx, userID, feed.ID); err != nil {
		t.Fatalf("CreateSubscription returned error: %v", err)
	}

	publishedAt := time.Date(2025, time.January, 2, 12, 0, 0, 0, time.UTC)
	postgresItem, _, err := store.UpsertItem(ctx, Item{
		FeedID: feed.ID, GUID: "postgres", Title: "Postgres replication guide",
		Summary: "Configure logical replication slots.", PublishedAt: &publishedAt,
	})
	if err != nil {
		t.Fatalf("UpsertItem returned error: %v", err)
	}
	olderAt := publishedAt.Add(-time.Hour)
	if _, _, err := store.UpsertItem(ctx, Item{FeedID: feed.ID, GUID: "go", Title: "Go release", PublishedAt: &olderAt}); err != nil {
		t.Fatalf("UpsertItem returned error: %v", err)
	}
	if _, _, err := store.UpsertItem(ctx, Item{FeedID: otherFeed.ID, GUID: "private", Title: "Postgres private", PublishedAt: &publishedAt}); err != nil {
		t.Fatalf("UpsertItem returned error: %v", err)
	}

	filter, err := itemquery.Parse(`text=search='postgres replication';read==false`)
	if err != nil {
		t.Fatal(err)
	}
	matches, err := store.QueryItemsForUser(ctx, userID, ItemQuery{Filter: filter, Sort: ItemQuerySortRelevance, Limit: 10})
	if err != nil {
		t.Fatalf("QueryItemsForUser returned error: %v", err)
	}
	if len(matches) != 1 || matches[0].ID != postgresItem.ID || matches[0].FeedTitle != feed.Title || matches[0].Rank <= 0 {
		t.Fatalf("matches = %#v", matches)
	}

	if err := store.MarkItemRead(ctx, userID, postgresItem.ID); err != nil {
		t.Fatalf("MarkItemRead returned error: %v", err)
	}
	filter, err = itemquery.Parse(`read==true`)
	if err != nil {
		t.Fatal(err)
	}
	matches, err = store.QueryItemsForUser(ctx, userID, ItemQuery{Filter: filter, Sort: ItemQuerySortNewest, Limit: 10})
	if err != nil {
		t.Fatalf("read QueryItemsForUser returned error: %v", err)
	}
	if len(matches) != 1 || matches[0].ID != postgresItem.ID || matches[0].ReadAt == nil {
		t.Fatalf("read matches = %#v", matches)
	}

	firstPage, err := store.QueryItemsForUser(ctx, userID, ItemQuery{Sort: ItemQuerySortNewest, Limit: 1})
	if err != nil {
		t.Fatalf("first page QueryItemsForUser returned error: %v", err)
	}
	if len(firstPage) != 1 {
		t.Fatalf("first page = %#v", firstPage)
	}
	secondPage, err := store.QueryItemsForUser(ctx, userID, ItemQuery{
		Sort:  ItemQuerySortNewest,
		Limit: 1,
		Cursor: &ItemQueryCursor{
			SortAt: firstPage[0].SortAt,
			ItemID: firstPage[0].ID,
		},
	})
	if err != nil {
		t.Fatalf("second page QueryItemsForUser returned error: %v", err)
	}
	if len(secondPage) != 1 || secondPage[0].ID == firstPage[0].ID {
		t.Fatalf("second page = %#v", secondPage)
	}
	previousPage, err := store.QueryItemsForUser(ctx, userID, ItemQuery{
		Sort:  ItemQuerySortNewest,
		Limit: 1,
		Cursor: &ItemQueryCursor{
			SortAt: secondPage[0].SortAt,
			ItemID: secondPage[0].ID,
			Before: true,
		},
	})
	if err != nil {
		t.Fatalf("previous page QueryItemsForUser returned error: %v", err)
	}
	if len(previousPage) != 1 || previousPage[0].ID != firstPage[0].ID {
		t.Fatalf("previous page = %#v, want %#v", previousPage, firstPage)
	}

	filter, err = itemquery.Parse(`feed.id==` + otherFeed.ID)
	if err != nil {
		t.Fatal(err)
	}
	matches, err = store.QueryItemsForUser(ctx, userID, ItemQuery{Filter: filter, Sort: ItemQuerySortNewest, Limit: 10})
	if err != nil {
		t.Fatalf("scoped QueryItemsForUser returned error: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("unsubscribed matches = %#v", matches)
	}
}

func TestPostgresStoreListFeedsDueRefresh(t *testing.T) {
	database := openTestDB(t)
	var legacyQueueExists bool
	if err := database.QueryRowContext(context.Background(), `SELECT to_regclass('feed_refresh_jobs') IS NOT NULL`).Scan(&legacyQueueExists); err != nil {
		t.Fatalf("check legacy queue table: %v", err)
	}
	if legacyQueueExists {
		t.Fatal("legacy refresh queue table still exists")
	}
	store := NewPostgresStore(database)
	ctx := context.Background()
	feed := testFeed(t, store, ctx)
	userID := testUserID(t, database, ctx)
	if err := store.CreateSubscription(ctx, userID, feed.ID); err != nil {
		t.Fatalf("CreateSubscription returned error: %v", err)
	}

	feeds, err := store.ListFeedsDueRefresh(ctx, time.Hour, 10)
	if err != nil {
		t.Fatalf("ListFeedsDueRefresh returned error: %v", err)
	}
	if len(feeds) != 1 || feeds[0].ID != feed.ID {
		t.Fatalf("due feeds = %#v, want feed %q", feeds, feed.ID)
	}

	feed.LastError = "permanent failure"
	if _, err := store.UpdateFeed(ctx, feed); err != nil {
		t.Fatalf("UpdateFeed returned error: %v", err)
	}
	feeds, err = store.ListFeedsDueRefresh(ctx, time.Hour, 10)
	if err != nil {
		t.Fatalf("ListFeedsDueRefresh after error returned error: %v", err)
	}
	if len(feeds) != 0 {
		t.Fatalf("due feeds after error = %#v, want none", feeds)
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
	if _, err := database.ExecContext(ctx, `TRUNCATE item_assets, assets, events, items, feeds, sessions, users RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate tables: %v", err)
	}
	return database
}
