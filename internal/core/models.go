package core

import (
	"encoding/json"
	"time"
)

type Feed struct {
	ID          string
	URL         string
	Title       string
	Description string
	SiteURL     string
	IconURL     string
	RefreshedAt *time.Time
	LastError   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type Item struct {
	ID          string
	FeedID      string
	GUID        string
	URL         string
	Title       string
	Summary     string
	Author      string
	PublishedAt *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type RefreshJob struct {
	FeedID     string
	LeaseToken string
	Attempts   int
}

type Event struct {
	ID        string
	Name      string
	Entity    string
	EntityID  *string
	Payload   json.RawMessage
	CreatedAt time.Time
}

const (
	EventFeedAdded   = "feed.added"
	EventFeedUpdated = "feed.updated"
	EventFeedError   = "feed.error"
	EventItemCreated = "item.created"
)
