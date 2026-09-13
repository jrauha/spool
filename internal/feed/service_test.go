package feed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/spool-reader/spool/internal/core"
)

func TestAddCreatesFeedAndRefreshesIt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<rss><channel><title>Example</title></channel></rss>`))
	}))
	defer server.Close()

	store := &refreshStore{}
	svc := NewService(store, server.Client())
	feed, err := svc.Add(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("Add returned error: %v", err)
	}
	if feed.ID == "" || store.feed.Title != "Example" {
		t.Fatalf("feed = %#v", store.feed)
	}
	if len(store.events) != 2 {
		t.Fatalf("events = %#v", store.events)
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
	feed   core.Feed
	items  []core.Item
	events []core.Event
}

func (s *refreshStore) CreateFeed(ctx context.Context, feed core.Feed) (core.Feed, error) {
	feed.ID = "feed-1"
	s.feed = feed
	return feed, nil
}

func (s *refreshStore) FindFeed(ctx context.Context, id string) (core.Feed, error) {
	return s.feed, nil
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
