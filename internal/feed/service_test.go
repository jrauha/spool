package feed

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/spool-reader/spool/internal/core"
)

func TestAddCreatesFeedAndQueuesRefresh(t *testing.T) {
	store := &refreshStore{}
	jobs := &refreshJobInserterFake{}
	svc := NewServiceWithJobs(store, jobs, nil)
	feed, err := svc.Add(context.Background(), "user-1", "https://example.com/feed.xml")
	if err != nil {
		t.Fatalf("Add returned error: %v", err)
	}
	if feed.ID == "" || store.subscriptionFeedID != feed.ID {
		t.Fatalf("feed = %#v", store.feed)
	}
	if len(jobs.args) != 1 || jobs.args[0].FeedID != feed.ID || jobs.args[0].FeedURL != feed.URL {
		t.Fatalf("queued args = %#v", jobs.args)
	}
	if len(store.events) != 1 {
		t.Fatalf("events = %#v", store.events)
	}
}

func TestFallbackIconURL(t *testing.T) {
	for _, test := range []struct {
		siteURL string
		want    string
	}{
		{siteURL: "https://example.com/posts", want: "https://example.com/favicon.ico"},
		{siteURL: "not a URL", want: ""},
	} {
		if got := fallbackIconURL(test.siteURL); got != test.want {
			t.Fatalf("fallbackIconURL(%q) = %q, want %q", test.siteURL, got, test.want)
		}
	}
}

func TestDeleteRemovesSubscriptionAndRecordsEvent(t *testing.T) {
	store := &refreshStore{feed: core.Feed{ID: "feed-1"}}
	svc := NewService(store, nil)

	if err := svc.Delete(context.Background(), "user-1", "feed-1"); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if store.deletedSubscriptionID != "feed-1" {
		t.Fatalf("deleted subscription ID = %q, want feed-1", store.deletedSubscriptionID)
	}
	if len(store.events) != 1 || store.events[0].Name != core.EventFeedDeleted {
		t.Fatalf("events = %#v", store.events)
	}
}

func TestQueueRefresh(t *testing.T) {
	store := &refreshStore{feed: core.Feed{ID: "feed-1", URL: "https://example.com/feed"}}
	jobs := &refreshJobInserterFake{}
	svc := NewServiceWithJobs(store, jobs, nil)

	if err := svc.QueueRefresh(context.Background(), store.feed.ID); err != nil {
		t.Fatalf("QueueRefresh returned error: %v", err)
	}
	if len(jobs.args) != 1 || jobs.args[0].FeedID != store.feed.ID {
		t.Fatalf("queued args = %#v", jobs.args)
	}
}

func TestRefreshJobSkipsStaleGeneration(t *testing.T) {
	store := &refreshStore{feed: core.Feed{ID: "feed-1", URL: "https://example.com/feed"}}
	svc := NewService(store, nil)
	if err := svc.RefreshJob(context.Background(), RefreshArgs{FeedID: store.feed.ID, FeedURL: store.feed.URL, Generation: 42}); err != nil {
		t.Fatalf("RefreshJob returned error: %v", err)
	}
	if len(store.events) != 0 {
		t.Fatalf("stale job caused events: %#v", store.events)
	}
}

func TestMarkReadAppendsEvent(t *testing.T) {
	store := &refreshStore{}
	svc := NewService(store, nil)

	if err := svc.MarkRead(context.Background(), "user-1", "item-1"); err != nil {
		t.Fatalf("MarkRead returned error: %v", err)
	}
	if store.readItemID != "item-1" {
		t.Fatalf("read item ID = %q, want item-1", store.readItemID)
	}
	if len(store.events) != 1 || store.events[0].Name != core.EventItemRead {
		t.Fatalf("events = %#v, want item read event", store.events)
	}
}

func TestMarkFeedReadAppendsEvent(t *testing.T) {
	store := &refreshStore{}
	svc := NewService(store, nil)

	if err := svc.MarkFeedRead(context.Background(), "user-1", "feed-1"); err != nil {
		t.Fatalf("MarkFeedRead returned error: %v", err)
	}
	if store.readFeedID != "feed-1" {
		t.Fatalf("read feed ID = %q, want feed-1", store.readFeedID)
	}
	if len(store.events) != 1 || store.events[0].Name != core.EventFeedRead {
		t.Fatalf("events = %#v, want feed read event", store.events)
	}
}

func TestQueryItemsUsesRSQLAndReturnsCursor(t *testing.T) {
	now := time.Now().UTC()
	store := &refreshStore{queryMatches: []core.ItemMatch{
		{Item: core.Item{ID: "item-1"}, SortAt: now, Rank: 0.8},
		{Item: core.Item{ID: "item-2"}, SortAt: now.Add(-time.Minute), Rank: 0.7},
	}}
	svc := NewService(store, nil)

	page, err := svc.QueryItemsForUser(context.Background(), "user-1", ItemQuery{
		Filter: `text=search='postgres';read==false`,
		Limit:  1,
	})
	if err != nil {
		t.Fatalf("QueryItemsForUser returned error: %v", err)
	}
	if len(page.Items) != 1 || page.PreviousCursor != "" || page.NextCursor == "" {
		t.Fatalf("page = %#v", page)
	}
	if store.itemQuery.Sort != core.ItemQuerySortRelevance || store.itemQuery.Limit != 2 || store.itemQuery.Filter == nil {
		t.Fatalf("store query = %#v", store.itemQuery)
	}

	store.queryMatches = []core.ItemMatch{{Item: core.Item{ID: "item-2"}, SortAt: now.Add(-time.Minute), Rank: 0.7}}
	secondPage, err := svc.QueryItemsForUser(context.Background(), "user-1", ItemQuery{
		Filter: `text=search='postgres';read==false`,
		Limit:  1,
		Cursor: page.NextCursor,
	})
	if err != nil {
		t.Fatalf("cursor query returned error: %v", err)
	}
	if store.itemQuery.Cursor == nil || store.itemQuery.Cursor.ItemID != "item-1" || store.itemQuery.Cursor.Before {
		t.Fatalf("store cursor = %#v", store.itemQuery.Cursor)
	}
	if secondPage.PreviousCursor == "" || secondPage.NextCursor != "" {
		t.Fatalf("second page = %#v", secondPage)
	}

	store.queryMatches = []core.ItemMatch{{Item: core.Item{ID: "item-1"}, SortAt: now, Rank: 0.8}}
	previousPage, err := svc.QueryItemsForUser(context.Background(), "user-1", ItemQuery{
		Filter: `text=search='postgres';read==false`,
		Limit:  1,
		Cursor: secondPage.PreviousCursor,
	})
	if err != nil {
		t.Fatalf("previous cursor query returned error: %v", err)
	}
	if store.itemQuery.Cursor == nil || !store.itemQuery.Cursor.Before || store.itemQuery.Cursor.ItemID != "item-2" {
		t.Fatalf("previous store cursor = %#v", store.itemQuery.Cursor)
	}
	if len(previousPage.Items) != 1 || previousPage.Items[0].ID != "item-1" ||
		previousPage.PreviousCursor != "" || previousPage.NextCursor == "" {
		t.Fatalf("previous page = %#v", previousPage)
	}
}

func TestQueryItemsAcceptsPlainText(t *testing.T) {
	store := &refreshStore{}
	svc := NewService(store, nil)

	if _, err := svc.QueryItemsForUser(context.Background(), "user-1", ItemQuery{Text: " postgres replication "}); err != nil {
		t.Fatalf("QueryItemsForUser returned error: %v", err)
	}
	if store.itemQuery.Sort != core.ItemQuerySortRelevance || store.itemQuery.Filter == nil {
		t.Fatalf("store query = %#v", store.itemQuery)
	}
	comparison := store.itemQuery.Filter.Conjunctions[0].Terms[0].Comparison
	if comparison.Selector != "text" || comparison.Values()[0] != "postgres replication" {
		t.Fatalf("comparison = %#v", comparison)
	}
}

func TestQueryItemsRejectsInvalidInput(t *testing.T) {
	svc := NewService(&refreshStore{}, nil)
	for _, input := range []ItemQuery{
		{Text: "postgres", Filter: "read==false"},
		{Filter: "title=bad=value"},
		{Filter: "unknown==value"},
		{Filter: "read>2025-01-01T00:00:00Z"},
		{Filter: "read==maybe"},
		{Filter: "feed.id==not-a-uuid"},
		{Filter: "date>=yesterday"},
		{Limit: maxItemQueryLimit + 1},
		{Sort: ItemSortRelevance},
		{Cursor: "not-a-cursor"},
	} {
		if _, err := svc.QueryItemsForUser(context.Background(), "user-1", input); !errors.Is(err, ErrInvalidQuery) {
			t.Fatalf("QueryItemsForUser(%#v) error = %v, want ErrInvalidQuery", input, err)
		}
	}
}

func TestQueryCursorCannotBeReusedWithDifferentFilter(t *testing.T) {
	now := time.Now().UTC()
	store := &refreshStore{queryMatches: []core.ItemMatch{
		{Item: core.Item{ID: "item-1"}, SortAt: now},
		{Item: core.Item{ID: "item-2"}, SortAt: now.Add(-time.Minute)},
	}}
	svc := NewService(store, nil)
	page, err := svc.QueryItemsForUser(context.Background(), "user-1", ItemQuery{Filter: "read==false", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.QueryItemsForUser(context.Background(), "user-1", ItemQuery{Filter: "read==true", Limit: 1, Cursor: page.NextCursor})
	if !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("cursor reuse error = %v, want ErrInvalidQuery", err)
	}
}

func TestValidFeedURLRejectsCredentialsAndUnsafePorts(t *testing.T) {
	for _, rawURL := range []string{"http://user:pass@example.com/feed", "https://example.com:8080/feed"} {
		parsedURL, err := url.Parse(rawURL)
		if err != nil {
			t.Fatal(err)
		}
		if validFeedURL(parsedURL) {
			t.Fatalf("validFeedURL(%q) = true", rawURL)
		}
	}
}

func TestRefreshUpdatesFeedAndCreatesItems(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<rss><channel>
			<title>Example</title><description>News</description><link>https://example.com</link>
			<item><guid>one</guid><title>First</title><link>https://example.com/one</link></item>
		</channel></rss>`))
	}))
	defer server.Close()

	store := &refreshStore{feed: core.Feed{ID: "feed-1", URL: server.URL}}
	svc := NewService(store, server.Client())

	if err := svc.Refresh(context.Background(), store.feed.ID); err != nil {
		t.Fatalf("Refresh returned error: %v", err)
	}
	if store.feed.Title != "Example" || store.feed.RefreshedAt == nil {
		t.Fatalf("feed = %#v", store.feed)
	}
	if len(store.items) != 1 || store.items[0].GUID != "one" {
		t.Fatalf("items = %#v", store.items)
	}
	if len(store.events) != 2 {
		t.Fatalf("events = %#v", store.events)
	}
}

func TestRefreshTruncatesOversizedItemFields(t *testing.T) {
	longTitle := strings.Repeat("界", core.MaxItemTitleChars+1)
	longAuthor := strings.Repeat("a", core.MaxItemAuthorChars+1)
	longSummary := strings.Repeat("s", core.MaxItemSummaryChars+1)
	longURL := "https://example.com/" + strings.Repeat("u", core.MaxItemURLChars)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<rss><channel><title>Example</title><item><guid>one</guid><title>` + longTitle +
			`</title><link>` + longURL + `</link><description>` + longSummary + `</description><author>` + longAuthor +
			`</author></item></channel></rss>`))
	}))
	defer server.Close()

	store := &refreshStore{feed: core.Feed{ID: "feed-1", URL: server.URL}}
	svc := NewService(store, server.Client())

	if err := svc.Refresh(context.Background(), store.feed.ID); err != nil {
		t.Fatalf("Refresh returned error: %v", err)
	}
	if len(store.items) != 1 {
		t.Fatalf("items = %#v", store.items)
	}
	item := store.items[0]
	for name, test := range map[string]struct {
		value string
		limit int
	}{
		"title":   {value: item.Title, limit: core.MaxItemTitleChars},
		"author":  {value: item.Author, limit: core.MaxItemAuthorChars},
		"summary": {value: item.Summary, limit: core.MaxItemSummaryChars},
		"URL":     {value: item.URL, limit: core.MaxItemURLChars},
	} {
		if length := utf8.RuneCountInString(test.value); length != test.limit {
			t.Errorf("%s length = %d, want %d", name, length, test.limit)
		}
	}
}

func TestRefreshRecordsPermanentHTTPError(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	store := &refreshStore{feed: core.Feed{ID: "feed-1", URL: server.URL}}
	svc := NewService(store, server.Client())

	err := svc.Refresh(context.Background(), store.feed.ID)
	if err == nil {
		t.Fatal("Refresh returned nil error")
	}
	if !isPermanentRefreshError(err) {
		t.Fatalf("Refresh error = %v, want permanent error", err)
	}
	if errors.Unwrap(err) == nil {
		t.Fatalf("Refresh error = %T, want wrapped error", err)
	}
	if store.feed.LastError == "" {
		t.Fatal("last error was not recorded")
	}
	if len(store.events) != 1 || store.events[0].Name != core.EventFeedError {
		t.Fatalf("events = %#v, want feed error event", store.events)
	}
}

func TestRefreshRecordsParseError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not xml`))
	}))
	defer server.Close()

	store := &refreshStore{feed: core.Feed{ID: "feed-1", URL: server.URL}}
	svc := NewService(store, server.Client())

	if err := svc.Refresh(context.Background(), store.feed.ID); err == nil {
		t.Fatal("Refresh returned nil error")
	}
	if store.feed.LastError == "" {
		t.Fatal("last error was not recorded")
	}
	if len(store.events) != 1 || store.events[0].Name != core.EventFeedError {
		t.Fatalf("events = %#v, want feed error event", store.events)
	}
}

func TestSafeDialContextRejectsLocalhost(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	conn, err := safeDialContext(ctx, "tcp", "127.0.0.1:80")
	if err == nil {
		conn.Close()
		t.Fatal("safeDialContext accepted localhost")
	}
}

type refreshJobInserterFake struct {
	args []RefreshArgs
}

func (f *refreshJobInserterFake) InsertRefresh(_ context.Context, args RefreshArgs) error {
	f.args = append(f.args, args)
	return nil
}

func (f *refreshJobInserterFake) InsertRefreshBatch(_ context.Context, args []RefreshArgs) error {
	f.args = append(f.args, args...)
	return nil
}

type refreshStore struct {
	feed                  core.Feed
	items                 []core.Item
	queryMatches          []core.ItemMatch
	itemQuery             core.ItemQuery
	events                []core.Event
	subscriptionFeedID    string
	deletedSubscriptionID string
	readItemID            string
	unreadItemID          string
	readFeedID            string
	allReadUserID         string
}

func (s *refreshStore) CreateFeed(ctx context.Context, feed core.Feed) (core.Feed, error) {
	feed.ID = "feed-1"
	s.feed = feed
	return feed, nil
}

func (s *refreshStore) FindOrCreateFeed(ctx context.Context, feed core.Feed) (core.Feed, bool, error) {
	feed.ID = "feed-1"
	s.feed = feed
	return feed, true, nil
}

func (s *refreshStore) CreateSubscription(ctx context.Context, userID, feedID string) error {
	s.subscriptionFeedID = feedID
	return nil
}

func (s *refreshStore) FindFeed(ctx context.Context, id string) (core.Feed, error) {
	return s.feed, nil
}

func (s *refreshStore) FindFeedForUser(ctx context.Context, userID, feedID string) (core.Feed, error) {
	return s.feed, nil
}

func (s *refreshStore) ListFeeds(ctx context.Context) ([]core.Feed, error) {
	return []core.Feed{s.feed}, nil
}

func (s *refreshStore) ListFeedsForUser(ctx context.Context, userID string) ([]core.Feed, error) {
	return []core.Feed{s.feed}, nil
}

func (s *refreshStore) DeleteFeed(ctx context.Context, id string) error {
	if id != s.feed.ID {
		return core.ErrFeedNotFound
	}
	s.feed = core.Feed{}
	return nil
}

func (s *refreshStore) DeleteSubscription(ctx context.Context, userID, feedID string) error {
	s.deletedSubscriptionID = feedID
	return nil
}

func (s *refreshStore) QueryItemsForUser(ctx context.Context, userID string, query core.ItemQuery) ([]core.ItemMatch, error) {
	s.itemQuery = query
	return s.queryMatches, nil
}

func (s *refreshStore) MarkItemRead(ctx context.Context, userID, itemID string) error {
	s.readItemID = itemID
	return nil
}

func (s *refreshStore) MarkItemUnread(ctx context.Context, userID, itemID string) error {
	s.unreadItemID = itemID
	return nil
}

func (s *refreshStore) MarkFeedRead(ctx context.Context, userID, feedID string) error {
	s.readFeedID = feedID
	return nil
}

func (s *refreshStore) MarkAllRead(ctx context.Context, userID string) error {
	s.allReadUserID = userID
	return nil
}

func (s *refreshStore) UpdateFeed(ctx context.Context, feed core.Feed) (core.Feed, error) {
	s.feed = feed
	return feed, nil
}

func (s *refreshStore) UpsertItem(ctx context.Context, item core.Item) (core.Item, bool, error) {
	item.ID = "item-1"
	s.items = append(s.items, item)
	return item, true, nil
}

func (s *refreshStore) AppendEvent(ctx context.Context, event core.Event) (core.Event, error) {
	event.Payload = append(json.RawMessage(nil), event.Payload...)
	s.events = append(s.events, event)
	return event, nil
}
