package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

var (
	ErrFeedNotFound = errors.New("feed not found")
	ErrItemNotFound = errors.New("item not found")
)

type PostgresStore struct {
	db *sql.DB
}

func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (s *PostgresStore) CreateFeed(ctx context.Context, feed Feed) (Feed, error) {
	row := s.db.QueryRowContext(ctx, `
		INSERT INTO feeds (url, title, description, site_url)
		VALUES ($1, $2, $3, $4)
		RETURNING id::text, url, title, description, site_url, refreshed_at,
			last_error, created_at, updated_at
	`, feed.URL, feed.Title, feed.Description, feed.SiteURL)
	return scanFeed(row)
}

func (s *PostgresStore) FindFeed(ctx context.Context, id string) (Feed, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id::text, url, title, description, site_url, refreshed_at,
			last_error, created_at, updated_at
		FROM feeds
		WHERE id = $1
	`, id)
	return scanFeed(row)
}

func (s *PostgresStore) ListFeeds(ctx context.Context) ([]Feed, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id::text, url, title, description, site_url, refreshed_at,
			last_error, created_at, updated_at
		FROM feeds
		ORDER BY title, id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	feeds := make([]Feed, 0)
	for rows.Next() {
		feed, err := scanFeed(rows)
		if err != nil {
			return nil, err
		}
		feeds = append(feeds, feed)
	}
	return feeds, rows.Err()
}

func (s *PostgresStore) UpsertItem(ctx context.Context, item Item) (Item, bool, error) {
	var created bool
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO items (feed_id, guid, url, title, summary, author, published_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (feed_id, identity_key) DO UPDATE SET
			guid = EXCLUDED.guid,
			url = EXCLUDED.url,
			title = EXCLUDED.title,
			summary = EXCLUDED.summary,
			author = EXCLUDED.author,
			published_at = EXCLUDED.published_at,
			updated_at = now()
		RETURNING id::text, feed_id::text, guid, url, title, summary, author,
			published_at, created_at, updated_at, xmax = 0
	`, item.FeedID, item.GUID, item.URL, item.Title, item.Summary, item.Author, item.PublishedAt).Scan(
		&item.ID,
		&item.FeedID,
		&item.GUID,
		&item.URL,
		&item.Title,
		&item.Summary,
		&item.Author,
		&item.PublishedAt,
		&item.CreatedAt,
		&item.UpdatedAt,
		&created,
	)
	return item, created, err
}

func (s *PostgresStore) FindItem(ctx context.Context, id string) (Item, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id::text, feed_id::text, guid, url, title, summary, author,
			published_at, created_at, updated_at
		FROM items
		WHERE id = $1
	`, id)
	return scanItem(row)
}

func (s *PostgresStore) ListItems(ctx context.Context, feedID string) ([]Item, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id::text, feed_id::text, guid, url, title, summary, author,
			published_at, created_at, updated_at
		FROM items
		WHERE feed_id = $1
		ORDER BY published_at DESC NULLS LAST, created_at DESC
	`, feedID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]Item, 0)
	for rows.Next() {
		item, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) AppendEvent(ctx context.Context, event Event) (Event, error) {
	payload := event.Payload
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}

	var storedPayload []byte
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO events (name, entity, entity_id, payload)
		VALUES ($1, $2, $3, $4)
		RETURNING id::text, name, entity, entity_id::text, payload, created_at
	`, event.Name, event.Entity, event.EntityID, payload).Scan(
		&event.ID,
		&event.Name,
		&event.Entity,
		&event.EntityID,
		&storedPayload,
		&event.CreatedAt,
	)
	event.Payload = storedPayload
	return event, err
}

type feedScanner interface {
	Scan(dest ...any) error
}

func scanFeed(row feedScanner) (Feed, error) {
	var feed Feed
	err := row.Scan(
		&feed.ID,
		&feed.URL,
		&feed.Title,
		&feed.Description,
		&feed.SiteURL,
		&feed.RefreshedAt,
		&feed.LastError,
		&feed.CreatedAt,
		&feed.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Feed{}, ErrFeedNotFound
	}
	return feed, err
}

func scanItem(row feedScanner) (Item, error) {
	var item Item
	err := row.Scan(
		&item.ID,
		&item.FeedID,
		&item.GUID,
		&item.URL,
		&item.Title,
		&item.Summary,
		&item.Author,
		&item.PublishedAt,
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Item{}, ErrItemNotFound
	}
	return item, err
}
