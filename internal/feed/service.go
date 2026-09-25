package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/spool-reader/spool/internal/core"
)

const (
	defaultRequestTimeout = 30 * time.Second
	maxFeedBytes          = 10 << 20
)

type permanentRefreshError struct {
	err error
}

func (e permanentRefreshError) Error() string {
	return e.err.Error()
}

func (e permanentRefreshError) Unwrap() error {
	return e.err
}

func (e permanentRefreshError) Permanent() bool {
	return true
}

type Store interface {
	CreateFeed(ctx context.Context, feed core.Feed) (core.Feed, error)
	FindOrCreateFeed(ctx context.Context, feed core.Feed) (core.Feed, bool, error)
	CreateSubscription(ctx context.Context, userID, feedID string) error
	DeleteFeed(ctx context.Context, id string) error
	DeleteSubscription(ctx context.Context, userID, feedID string) error
	FindFeed(ctx context.Context, id string) (core.Feed, error)
	FindFeedForUser(ctx context.Context, userID, feedID string) (core.Feed, error)
	ListFeeds(ctx context.Context) ([]core.Feed, error)
	ListFeedsForUser(ctx context.Context, userID string) ([]core.Feed, error)
	ListItems(ctx context.Context, feedID string, limit, offset int) ([]core.Item, error)
	ListItemsForUser(ctx context.Context, userID, feedID string, limit, offset int) ([]core.Item, error)
	ListLatestItems(ctx context.Context, limit, offset int) ([]core.Item, error)
	ListLatestItemsForUser(ctx context.Context, userID string, limit, offset int) ([]core.Item, error)
	MarkItemRead(ctx context.Context, userID, itemID string) error
	MarkItemUnread(ctx context.Context, userID, itemID string) error
	MarkFeedRead(ctx context.Context, userID, feedID string) error
	MarkAllRead(ctx context.Context, userID string) error
	UpdateFeed(ctx context.Context, feed core.Feed) (core.Feed, error)
	UpsertItem(ctx context.Context, item core.Item) (core.Item, bool, error)
	AppendEvent(ctx context.Context, event core.Event) (core.Event, error)
}

type Service struct {
	store  Store
	jobs   RefreshJobInserter
	client *http.Client
}

func NewService(store Store, client *http.Client) *Service {
	return NewServiceWithJobs(store, nil, client)
}

func NewServiceWithJobs(store Store, jobs RefreshJobInserter, client *http.Client) *Service {
	if client == nil {
		client = safeHTTPClient()
	}
	return &Service{store: store, jobs: jobs, client: client}
}

func (s *Service) ListFeeds(ctx context.Context) ([]core.Feed, error) {
	return s.store.ListFeeds(ctx)
}

func (s *Service) ListFeedsForUser(ctx context.Context, userID string) ([]core.Feed, error) {
	return s.store.ListFeedsForUser(ctx, userID)
}

func (s *Service) Find(ctx context.Context, id string) (core.Feed, error) {
	return s.store.FindFeed(ctx, id)
}

func (s *Service) FindForUser(ctx context.Context, userID, id string) (core.Feed, error) {
	return s.store.FindFeedForUser(ctx, userID, id)
}

func (s *Service) Latest(ctx context.Context, limit, offset int) ([]core.Item, error) {
	return s.store.ListLatestItems(ctx, limit, offset)
}

func (s *Service) LatestForUser(ctx context.Context, userID string, limit, offset int) ([]core.Item, error) {
	return s.store.ListLatestItemsForUser(ctx, userID, limit, offset)
}

func (s *Service) Items(ctx context.Context, feedID string, limit, offset int) ([]core.Item, error) {
	return s.store.ListItems(ctx, feedID, limit, offset)
}

func (s *Service) ItemsForUser(ctx context.Context, userID, feedID string, limit, offset int) ([]core.Item, error) {
	return s.store.ListItemsForUser(ctx, userID, feedID, limit, offset)
}

func (s *Service) MarkRead(ctx context.Context, userID, itemID string) error {
	if err := s.store.MarkItemRead(ctx, userID, itemID); err != nil {
		return err
	}
	return s.appendEvent(ctx, core.EventItemRead, "item", itemID)
}

func (s *Service) MarkUnread(ctx context.Context, userID, itemID string) error {
	if err := s.store.MarkItemUnread(ctx, userID, itemID); err != nil {
		return err
	}
	return s.appendEvent(ctx, core.EventItemUnread, "item", itemID)
}

func (s *Service) MarkFeedRead(ctx context.Context, userID, feedID string) error {
	if err := s.store.MarkFeedRead(ctx, userID, feedID); err != nil {
		return err
	}
	return s.appendEvent(ctx, core.EventFeedRead, "feed", feedID)
}

func (s *Service) MarkAllRead(ctx context.Context, userID string) error {
	if err := s.store.MarkAllRead(ctx, userID); err != nil {
		return err
	}
	return s.appendEvent(ctx, core.EventFeedsRead, "user", userID)
}

func (s *Service) Delete(ctx context.Context, userID, id string) error {
	if err := s.store.DeleteSubscription(ctx, userID, id); err != nil {
		return err
	}
	return s.appendEvent(ctx, core.EventFeedDeleted, "feed", id)
}

func (s *Service) QueueRefresh(ctx context.Context, id string) error {
	feed, err := s.store.FindFeed(ctx, id)
	if err != nil {
		return err
	}
	return s.enqueueRefresh(ctx, feed)
}

func (s *Service) Add(ctx context.Context, userID, rawURL string) (core.Feed, error) {
	parsedURL, err := url.ParseRequestURI(rawURL)
	if err != nil || !validFeedURL(parsedURL) {
		return core.Feed{}, fmt.Errorf("invalid feed URL")
	}

	feed, _, err := s.store.FindOrCreateFeed(ctx, core.Feed{URL: rawURL, Title: rawURL})
	if err != nil {
		return core.Feed{}, err
	}
	if err := s.store.CreateSubscription(ctx, userID, feed.ID); err != nil {
		return core.Feed{}, err
	}
	if err := s.appendEvent(ctx, core.EventFeedAdded, "feed", feed.ID); err != nil {
		return core.Feed{}, err
	}
	if err := s.enqueueRefresh(ctx, feed); err != nil {
		return core.Feed{}, err
	}
	return feed, nil
}

func (s *Service) Refresh(ctx context.Context, id string) error {
	feed, err := s.store.FindFeed(ctx, id)
	if err != nil {
		return err
	}
	return s.refresh(ctx, feed)
}

func (s *Service) RefreshJob(ctx context.Context, args RefreshArgs) error {
	feed, err := s.store.FindFeed(ctx, args.FeedID)
	if err != nil {
		return err
	}
	if feed.URL != args.FeedURL || refreshArgs(feed.ID, feed.URL, feed.RefreshedAt).Generation != args.Generation {
		return nil
	}
	return s.refresh(ctx, feed)
}

func (s *Service) refresh(ctx context.Context, feed core.Feed) error {
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
		err := fmt.Errorf("feed request returned %s", resp.Status)
		if resp.StatusCode >= http.StatusBadRequest && resp.StatusCode < http.StatusInternalServerError && resp.StatusCode != http.StatusRequestTimeout && resp.StatusCode != http.StatusTooManyRequests {
			err = permanentRefreshError{err: err}
		}
		return s.recordError(ctx, feed, err)
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
	feed.IconURL = parsed.IconURL
	if feed.IconURL == "" {
		feed.IconURL = fallbackIconURL(feed.SiteURL)
	}
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

func (s *Service) enqueueRefresh(ctx context.Context, feed core.Feed) error {
	if s.jobs == nil {
		return fmt.Errorf("refresh queue unavailable")
	}
	return s.jobs.InsertRefresh(ctx, refreshArgs(feed.ID, feed.URL, feed.RefreshedAt))
}

func validFeedURL(parsedURL *url.URL) bool {
	return parsedURL.Host != "" && parsedURL.User == nil &&
		(parsedURL.Scheme == "http" || parsedURL.Scheme == "https") &&
		(parsedURL.Port() == "" || parsedURL.Port() == "80" || parsedURL.Port() == "443")
}

func safeHTTPClient() *http.Client {
	return &http.Client{
		Timeout:   defaultRequestTimeout,
		Transport: &http.Transport{Proxy: nil, DialContext: safeDialContext},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if !validFeedURL(req.URL) {
				return fmt.Errorf("unsafe feed redirect")
			}
			return nil
		},
	}
}

func safeDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("resolve feed host: %w", err)
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("feed host has no addresses")
	}
	for _, ip := range addresses {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
			return nil, fmt.Errorf("feed host resolves to a non-public address")
		}
	}
	return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(addresses[0].String(), port))
}

func fallbackIconURL(siteURL string) string {
	parsedURL, err := url.ParseRequestURI(siteURL)
	if err != nil || parsedURL.Host == "" || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		return ""
	}
	parsedURL.Path = "/favicon.ico"
	parsedURL.RawQuery = ""
	parsedURL.Fragment = ""
	return parsedURL.String()
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
