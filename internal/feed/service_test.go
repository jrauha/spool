package feed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/spool-reader/spool/internal/core"
)

func TestAddCreatesFeedAndQueuesRefresh(t *testing.T) {
	store := &refreshStore{}
	svc := NewService(store, nil)
	feed, err := svc.Add(context.Background(), "user-1", "https://example.com/feed.xml")
	if err != nil {
		t.Fatalf("Add returned error: %v", err)
	}
	if feed.ID == "" || store.queuedFeedID != feed.ID || store.subscriptionFeedID != feed.ID {
		t.Fatalf("feed = %#v", store.feed)
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
	store := &refreshStore{feed: core.Feed{ID: "feed-1"}}
	svc := NewService(store, nil)

	if err := svc.QueueRefresh(context.Background(), store.feed.ID); err != nil {
		t.Fatalf("QueueRefresh returned error: %v", err)
	}
	if store.queuedFeedID != store.feed.ID {
		t.Fatalf("queued feed ID = %q, want %q", store.queuedFeedID, store.feed.ID)
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

type refreshStore struct {
	feed                  core.Feed
	items                 []core.Item
	events                []core.Event
	queuedFeedID          string
	subscriptionFeedID    string
	deletedSubscriptionID string
	readItemID            string
	unreadItemID          string
	readFeedID            string
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

func (s *refreshStore) ListItems(ctx context.Context, feedID string, limit, offset int) ([]core.Item, error) {
	return s.items, nil
}

func (s *refreshStore) ListItemsForUser(ctx context.Context, userID, feedID string, limit, offset int) ([]core.Item, error) {
	return s.items, nil
}

func (s *refreshStore) ListLatestItems(ctx context.Context, limit, offset int) ([]core.Item, error) {
	return s.items, nil
}

func (s *refreshStore) ListLatestItemsForUser(ctx context.Context, userID string, limit, offset int) ([]core.Item, error) {
	return s.items, nil
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
	return nil
}

func (s *refreshStore) EnqueueRefresh(ctx context.Context, feedID string, availableAt time.Time) error {
	s.queuedFeedID = feedID
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
