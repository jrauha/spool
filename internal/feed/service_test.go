package feed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/spool-reader/spool/internal/core"
)

func TestAddCreatesFeedAndQueuesRefresh(t *testing.T) {
	store := &refreshStore{}
	svc := NewService(store, nil)
	feed, err := svc.Add(context.Background(), "https://example.com/feed.xml")
	if err != nil {
		t.Fatalf("Add returned error: %v", err)
	}
	if feed.ID == "" || store.queuedFeedID != feed.ID {
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

func TestDeleteRemovesFeedAndRecordsEvent(t *testing.T) {
	store := &refreshStore{feed: core.Feed{ID: "feed-1"}}
	svc := NewService(store, nil)

	if err := svc.Delete(context.Background(), "feed-1"); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if store.feed.ID != "" {
		t.Fatalf("feed = %#v, want deleted", store.feed)
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
	feed         core.Feed
	items        []core.Item
	events       []core.Event
	queuedFeedID string
}

func (s *refreshStore) CreateFeed(ctx context.Context, feed core.Feed) (core.Feed, error) {
	feed.ID = "feed-1"
	s.feed = feed
	return feed, nil
}

func (s *refreshStore) FindFeed(ctx context.Context, id string) (core.Feed, error) {
	return s.feed, nil
}

func (s *refreshStore) ListFeeds(ctx context.Context) ([]core.Feed, error) {
	return []core.Feed{s.feed}, nil
}

func (s *refreshStore) DeleteFeed(ctx context.Context, id string) error {
	if id != s.feed.ID {
		return core.ErrFeedNotFound
	}
	s.feed = core.Feed{}
	return nil
}

func (s *refreshStore) ListItems(ctx context.Context, feedID string, limit, offset int) ([]core.Item, error) {
	return s.items, nil
}

func (s *refreshStore) ListLatestItems(ctx context.Context, limit, offset int) ([]core.Item, error) {
	return s.items, nil
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
