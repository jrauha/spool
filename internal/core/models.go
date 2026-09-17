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
	UnreadCount int
	ReadBefore  *time.Time
	RefreshedAt *time.Time
	LastError   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type Subscription struct {
	ID         string
	UserID     string
	FeedID     string
	ReadBefore *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
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
	ReadAt      *time.Time
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
	EventFeedDeleted = "feed.deleted"
	EventFeedError   = "feed.error"
	EventFeedRead    = "feed.read"
	EventFeedsRead   = "feeds.read"
	EventItemCreated = "item.created"
	EventItemRead    = "item.read"
	EventItemUnread  = "item.unread"
)
