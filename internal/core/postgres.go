package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
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
		INSERT INTO feeds (url, title, description, site_url, icon_url)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id::text, url, title, description, site_url, icon_url, refreshed_at,
			last_error, created_at, updated_at
	`, feed.URL, feed.Title, feed.Description, feed.SiteURL, feed.IconURL)
	return scanFeed(row)
}

func (s *PostgresStore) FindOrCreateFeed(ctx context.Context, feed Feed) (Feed, bool, error) {
	var created bool
	row := s.db.QueryRowContext(ctx, `
		WITH inserted AS (
			INSERT INTO feeds (url, title, description, site_url, icon_url)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (url) DO NOTHING
			RETURNING id::text, url, title, description, site_url, icon_url, refreshed_at,
				last_error, created_at, updated_at, true
		)
		SELECT * FROM inserted
		UNION ALL
		SELECT id::text, url, title, description, site_url, icon_url, refreshed_at,
			last_error, created_at, updated_at, false
		FROM feeds
		WHERE url = $1 AND NOT EXISTS (SELECT 1 FROM inserted)
		LIMIT 1
	`, feed.URL, feed.Title, feed.Description, feed.SiteURL, feed.IconURL)
	err := row.Scan(
		&feed.ID,
		&feed.URL,
		&feed.Title,
		&feed.Description,
		&feed.SiteURL,
		&feed.IconURL,
		&feed.RefreshedAt,
		&feed.LastError,
		&feed.CreatedAt,
		&feed.UpdatedAt,
		&created,
	)
	return feed, created, err
}

func (s *PostgresStore) UpdateFeed(ctx context.Context, feed Feed) (Feed, error) {
	row := s.db.QueryRowContext(ctx, `
		UPDATE feeds
		SET title = $2, description = $3, site_url = $4, icon_url = $5,
			refreshed_at = $6, last_error = $7, updated_at = now()
		WHERE id = $1
		RETURNING id::text, url, title, description, site_url, icon_url, refreshed_at,
			last_error, created_at, updated_at
	`, feed.ID, feed.Title, feed.Description, feed.SiteURL, feed.IconURL, feed.RefreshedAt, feed.LastError)
	return scanFeed(row)
}

func (s *PostgresStore) CreateSubscription(ctx context.Context, userID, feedID string) error {
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO subscriptions (user_id, feed_id)
		SELECT $1, id FROM feeds WHERE id = $2
		ON CONFLICT (user_id, feed_id) DO NOTHING
	`, userID, feedID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil || count != 0 {
		return err
	}
	_, err = s.FindFeed(ctx, feedID)
	return err
}

func (s *PostgresStore) DeleteSubscription(ctx context.Context, userID, feedID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
		DELETE FROM subscriptions WHERE user_id = $1 AND feed_id = $2
	`, userID, feedID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrFeedNotFound
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM item_read_overrides
		USING items
		WHERE item_read_overrides.user_id = $1
			AND item_read_overrides.item_id = items.id
			AND items.feed_id = $2
	`, userID, feedID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresStore) FindFeedForUser(ctx context.Context, userID, feedID string) (Feed, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT feeds.id::text, feeds.url, feeds.title, feeds.description, feeds.site_url,
			feeds.icon_url, subscriptions.read_before, feeds.refreshed_at,
			feeds.last_error, feeds.created_at, feeds.updated_at,
			count(items.id) FILTER (WHERE CASE
				WHEN read_override.is_read THEN read_override.read_at
				WHEN read_override.is_read = false THEN NULL
				WHEN subscriptions.read_before IS NOT NULL AND items.created_at <= subscriptions.read_before THEN subscriptions.read_before
				ELSE NULL
			END IS NULL)::int
		FROM subscriptions
		JOIN feeds ON feeds.id = subscriptions.feed_id
		LEFT JOIN items ON items.feed_id = feeds.id
		LEFT JOIN item_read_overrides read_override
			ON read_override.item_id = items.id AND read_override.user_id = subscriptions.user_id
		WHERE subscriptions.user_id = $1 AND subscriptions.feed_id = $2
		GROUP BY feeds.id, subscriptions.read_before
	`, userID, feedID)
	return scanSubscribedFeed(row)
}

func (s *PostgresStore) ListFeedsForUser(ctx context.Context, userID string) ([]Feed, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT feeds.id::text, feeds.url, feeds.title, feeds.description, feeds.site_url,
			feeds.icon_url, subscriptions.read_before, feeds.refreshed_at,
			feeds.last_error, feeds.created_at, feeds.updated_at,
			count(items.id) FILTER (WHERE CASE
				WHEN read_override.is_read THEN read_override.read_at
				WHEN read_override.is_read = false THEN NULL
				WHEN subscriptions.read_before IS NOT NULL AND items.created_at <= subscriptions.read_before THEN subscriptions.read_before
				ELSE NULL
			END IS NULL)::int
		FROM subscriptions
		JOIN feeds ON feeds.id = subscriptions.feed_id
		LEFT JOIN items ON items.feed_id = feeds.id
		LEFT JOIN item_read_overrides read_override
			ON read_override.item_id = items.id AND read_override.user_id = subscriptions.user_id
		WHERE subscriptions.user_id = $1
		GROUP BY feeds.id, subscriptions.read_before
		ORDER BY feeds.title, feeds.created_at
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	feeds := make([]Feed, 0)
	for rows.Next() {
		feed, err := scanSubscribedFeed(rows)
		if err != nil {
			return nil, err
		}
		feeds = append(feeds, feed)
	}
	return feeds, rows.Err()
}

func (s *PostgresStore) DeleteFeed(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM feeds WHERE id = $1`, id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrFeedNotFound
	}
	return nil
}

func (s *PostgresStore) FindFeed(ctx context.Context, id string) (Feed, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id::text, url, title, description, site_url, icon_url, refreshed_at,
			last_error, created_at, updated_at
		FROM feeds
		WHERE id = $1
	`, id)
	return scanFeed(row)
}

func (s *PostgresStore) ListFeeds(ctx context.Context) ([]Feed, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id::text, url, title, description, site_url, icon_url, refreshed_at,
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

func (s *PostgresStore) ListFeedsDueRefresh(ctx context.Context, interval time.Duration, limit int) ([]Feed, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT feeds.id::text, feeds.url, feeds.title, feeds.description, feeds.site_url,
			feeds.icon_url, feeds.refreshed_at, feeds.last_error, feeds.created_at, feeds.updated_at
		FROM feeds
		WHERE EXISTS (SELECT 1 FROM subscriptions WHERE subscriptions.feed_id = feeds.id)
			AND feeds.last_error = ''
			AND (feeds.refreshed_at IS NULL
				OR feeds.refreshed_at <= now() - $1 * interval '1 second')
		ORDER BY feeds.refreshed_at ASC NULLS FIRST, feeds.id
		LIMIT $2
	`, interval.Seconds(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	feeds := make([]Feed, 0, limit)
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

func (s *PostgresStore) MarkItemRead(ctx context.Context, userID, itemID string) error {
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO item_read_overrides (user_id, item_id, is_read, read_at)
		SELECT $1, items.id, true, now()
		FROM items
		JOIN subscriptions ON subscriptions.feed_id = items.feed_id
		WHERE subscriptions.user_id = $1 AND items.id = $2
		ON CONFLICT (user_id, item_id) DO UPDATE
		SET is_read = true, read_at = now(), updated_at = now()
	`, userID, itemID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrItemNotFound
	}
	return nil
}

func (s *PostgresStore) MarkItemUnread(ctx context.Context, userID, itemID string) error {
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO item_read_overrides (user_id, item_id, is_read, read_at)
		SELECT $1, items.id, false, NULL::timestamptz
		FROM items
		JOIN subscriptions ON subscriptions.feed_id = items.feed_id
		WHERE subscriptions.user_id = $1 AND items.id = $2
		ON CONFLICT (user_id, item_id) DO UPDATE
		SET is_read = false, read_at = NULL, updated_at = now()
	`, userID, itemID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrItemNotFound
	}
	return nil
}

func (s *PostgresStore) MarkFeedRead(ctx context.Context, userID, feedID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
		UPDATE subscriptions
		SET read_before = now(), updated_at = now()
		WHERE user_id = $1 AND feed_id = $2
	`, userID, feedID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrFeedNotFound
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM item_read_overrides
		USING items
		WHERE item_read_overrides.user_id = $1
			AND item_read_overrides.item_id = items.id
			AND items.feed_id = $2
			AND item_read_overrides.is_read = false
	`, userID, feedID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresStore) MarkAllRead(ctx context.Context, userID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		UPDATE subscriptions
		SET read_before = now(), updated_at = now()
		WHERE user_id = $1
	`, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM item_read_overrides
		WHERE user_id = $1 AND is_read = false
	`, userID); err != nil {
		return err
	}
	return tx.Commit()
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
		&feed.IconURL,
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

func scanSubscribedFeed(row feedScanner) (Feed, error) {
	var feed Feed
	err := row.Scan(
		&feed.ID,
		&feed.URL,
		&feed.Title,
		&feed.Description,
		&feed.SiteURL,
		&feed.IconURL,
		&feed.ReadBefore,
		&feed.RefreshedAt,
		&feed.LastError,
		&feed.CreatedAt,
		&feed.UpdatedAt,
		&feed.UnreadCount,
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
