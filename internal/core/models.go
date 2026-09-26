package core

import (
	"encoding/json"
	"time"

	"github.com/spool-reader/spool/internal/query"
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

const (
	MaxItemTitleChars   = 1000
	MaxItemAuthorChars  = 500
	MaxItemSummaryChars = 100000
	MaxItemURLChars     = 4096

	ItemAssetRoleThumbnail = "thumbnail"
)

type Item struct {
	ID          string
	FeedID      string
	GUID        string
	URL         string
	ImageURL    string
	Title       string
	Summary     string
	Author      string
	Assets      []AssetAttachment
	PublishedAt *time.Time
	ReadAt      *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type Asset struct {
	ID        string
	MediaType string
	ByteSize  int64
	SHA256    string
	CreatedAt time.Time
}

type AssetAttachment struct {
	AssetID   string `json:"assetId"`
	Role      string `json:"role"`
	MediaType string `json:"mediaType"`
}

type ItemImageInputs struct {
	PageURL  string
	ImageURL string
}

type ItemMatch struct {
	Item
	FeedTitle string
	SortAt    time.Time
	Rank      float64
}

type ItemQuerySort string

const (
	ItemQuerySortNewest    ItemQuerySort = "newest"
	ItemQuerySortOldest    ItemQuerySort = "oldest"
	ItemQuerySortRelevance ItemQuerySort = "relevance"
)

type ItemQueryCursor struct {
	SortAt time.Time
	ItemID string
	Rank   float64
	Before bool
}

type ItemQuery struct {
	Filter *query.Expression
	Sort   ItemQuerySort
	Limit  int
	Cursor *ItemQueryCursor
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
