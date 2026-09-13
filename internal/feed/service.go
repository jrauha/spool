package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/spool-reader/spool/internal/core"
)

const (
	defaultRequestTimeout = 30 * time.Second
	maxFeedBytes          = 10 << 20
)

type Store interface {
	CreateFeed(ctx context.Context, feed core.Feed) (core.Feed, error)
	FindFeed(ctx context.Context, id string) (core.Feed, error)
	ListFeeds(ctx context.Context) ([]core.Feed, error)
	ListLatestItems(ctx context.Context, limit int) ([]core.Item, error)
	UpdateFeed(ctx context.Context, feed core.Feed) (core.Feed, error)
	UpsertItem(ctx context.Context, item core.Item) (core.Item, bool, error)
	AppendEvent(ctx context.Context, event core.Event) (core.Event, error)
}

type Service struct {
	store  Store
	client *http.Client
}

func NewService(store Store, client *http.Client) *Service {
	if client == nil {
		client = &http.Client{Timeout: defaultRequestTimeout}
	}
	return &Service{store: store, client: client}
}

func (s *Service) ListFeeds(ctx context.Context) ([]core.Feed, error) {
	return s.store.ListFeeds(ctx)
}

func (s *Service) Latest(ctx context.Context, limit int) ([]core.Item, error) {
	return s.store.ListLatestItems(ctx, limit)
}

func (s *Service) Add(ctx context.Context, rawURL string) (core.Feed, error) {
	parsedURL, err := url.ParseRequestURI(rawURL)
	if err != nil || parsedURL.Host == "" || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		return core.Feed{}, fmt.Errorf("invalid feed URL")
	}

	feed, err := s.store.CreateFeed(ctx, core.Feed{URL: rawURL, Title: rawURL})
	if err != nil {
		return core.Feed{}, err
	}
	if err := s.appendEvent(ctx, core.EventFeedAdded, "feed", feed.ID); err != nil {
		return core.Feed{}, err
	}
	if err := s.Refresh(ctx, feed.ID); err != nil {
		return feed, err
	}
	return s.store.FindFeed(ctx, feed.ID)
}

func (s *Service) Refresh(ctx context.Context, id string) error {
	feed, err := s.store.FindFeed(ctx, id)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feed.URL, nil)
	if err != nil {
		return s.recordError(ctx, feed, err)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return s.recordError(ctx, feed, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return s.recordError(ctx, feed, fmt.Errorf("feed request returned %s", resp.Status))
	}

	parsed, err := Parse(io.LimitReader(resp.Body, maxFeedBytes))
	if err != nil {
		return s.recordError(ctx, feed, err)
	}

	if parsed.Title != "" {
		feed.Title = parsed.Title
	}
	feed.Description = parsed.Description
	feed.SiteURL = parsed.SiteURL
	feed.LastError = ""
	now := time.Now().UTC()
	feed.RefreshedAt = &now
	feed, err = s.store.UpdateFeed(ctx, feed)
	if err != nil {
		return err
	}
	if err := s.appendEvent(ctx, core.EventFeedUpdated, "feed", feed.ID); err != nil {
		return err
	}

	for _, parsedItem := range parsed.Items {
		if parsedItem.Title == "" {
			continue
		}
		item, created, err := s.store.UpsertItem(ctx, core.Item{
			FeedID:      feed.ID,
			GUID:        parsedItem.GUID,
			URL:         parsedItem.URL,
			Title:       parsedItem.Title,
			Summary:     parsedItem.Summary,
			Author:      parsedItem.Author,
			PublishedAt: parsedItem.PublishedAt,
		})
		if err != nil {
			return err
		}
		if created {
			if err := s.appendEvent(ctx, core.EventItemCreated, "item", item.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) recordError(ctx context.Context, feed core.Feed, refreshErr error) error {
	feed.LastError = refreshErr.Error()
	_, err := s.store.UpdateFeed(ctx, feed)
	if err != nil {
		return err
	}
	if err := s.appendEvent(ctx, core.EventFeedError, "feed", feed.ID); err != nil {
		return err
	}
	return refreshErr
}

func (s *Service) appendEvent(ctx context.Context, name, entity, entityID string) error {
	payload, err := json.Marshal(map[string]string{"id": entityID})
	if err != nil {
		return err
	}
	_, err = s.store.AppendEvent(ctx, core.Event{
		Name:     name,
		Entity:   entity,
		EntityID: &entityID,
		Payload:  payload,
	})
	return err
}
