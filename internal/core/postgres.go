package core

import (
	"context"
	"database/sql"
	"errors"
)

var ErrFeedNotFound = errors.New("feed not found")

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
