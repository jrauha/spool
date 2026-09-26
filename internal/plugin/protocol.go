package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"time"

	"github.com/spool-reader/spool/internal/safehttp"
)

const (
	EventItemCreated            = "item.created"
	EventItemImageInputsChanged = "item.image_inputs_changed"
	EventItemRead               = "item.read"
	EventItemUnread             = "item.unread"
	EventItemStarred            = "item.starred"
	EventItemUnstarred          = "item.unstarred"
	EventFeedAdded              = "feed.added"
	EventFeedUpdated            = "feed.updated"
	EventFeedDeleted            = "feed.deleted"
	EventFeedError              = "feed.error"
	EventFeedRead               = "feed.read"
	EventFeedsRead              = "feeds.read"

	OperationFieldSet       = "field.set"
	OperationFieldDelete    = "field.delete"
	OperationAssetAttach    = "asset.attach"
	OperationAssetDetach    = "asset.detach"
	OperationItemMarkRead   = "item.mark_read"
	OperationItemMarkUnread = "item.mark_unread"
	OperationItemStar       = "item.star"
	OperationItemUnstar     = "item.unstar"
)

type Invocation struct {
	APIVersion string          `json:"apiVersion"`
	DeliveryID string          `json:"deliveryId"`
	Plugin     PluginInfo      `json:"plugin"`
	Event      EventInvocation `json:"event"`
}

type PluginInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	DataDir string `json:"dataDir"`
}

type EventInvocation struct {
	Name      string        `json:"name"`
	Timestamp time.Time     `json:"timestamp"`
	Feed      *FeedSnapshot `json:"feed,omitempty"`
	Item      *ItemSnapshot `json:"item,omitempty"`
}

type FeedSnapshot struct {
	ID      string `json:"id"`
	URL     string `json:"url"`
	Title   string `json:"title"`
	SiteURL string `json:"siteUrl,omitempty"`
}

type ItemSnapshot struct {
	ID          string     `json:"id"`
	FeedID      string     `json:"feedId"`
	URL         string     `json:"url"`
	ImageURL    string     `json:"imageUrl,omitempty"`
	Title       string     `json:"title"`
	Summary     string     `json:"summary,omitempty"`
	Author      string     `json:"author,omitempty"`
	PublishedAt *time.Time `json:"publishedAt,omitempty"`
}

type Response struct {
	Ops []Operation `json:"ops"`
}

type Operation struct {
	Op       string          `json:"op"`
	Entity   string          `json:"entity,omitempty"`
	EntityID string          `json:"entityId,omitempty"`
	Name     string          `json:"name,omitempty"`
	Value    json.RawMessage `json:"value,omitempty"`
	ItemID   string          `json:"itemId,omitempty"`
	Role     string          `json:"role,omitempty"`
	URL      string          `json:"url,omitempty"`
}

func DecodeInvocation(r io.Reader) (Invocation, error) {
	var invocation Invocation
	if err := decodeProtocolJSON(r, &invocation); err != nil {
		return Invocation{}, err
	}
	if err := invocation.Validate(); err != nil {
		return Invocation{}, err
	}
	return invocation, nil
}

func DecodeResponse(r io.Reader) (Response, error) {
	var response Response
	if err := decodeProtocolJSON(r, &response); err != nil {
		return Response{}, err
	}
	if err := response.Validate(); err != nil {
		return Response{}, err
	}
	return response, nil
}

func decodeProtocolJSON(r io.Reader, value any) error {
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("plugin protocol contains trailing JSON")
		}
		return err
	}
	return nil
}

func (invocation Invocation) Validate() error {
	if invocation.APIVersion != APIVersion {
		return fmt.Errorf("unsupported plugin apiVersion %q", invocation.APIVersion)
	}
	if invocation.DeliveryID == "" {
		return errors.New("plugin invocation requires deliveryId")
	}
	if invocation.Plugin.Name == "" || invocation.Plugin.Version == "" || invocation.Plugin.DataDir == "" {
		return errors.New("plugin invocation requires plugin name, version, and dataDir")
	}
	if invocation.Event.Name == "" || invocation.Event.Timestamp.IsZero() {
		return errors.New("plugin invocation requires event name and timestamp")
	}
	if invocation.Event.Name == EventItemCreated || invocation.Event.Name == EventItemImageInputsChanged {
		if invocation.Event.Feed == nil || invocation.Event.Item == nil {
			return fmt.Errorf("event %q requires feed and item data", invocation.Event.Name)
		}
		if invocation.Event.Item.FeedID != invocation.Event.Feed.ID {
			return errors.New("item feedId does not match event feed id")
		}
	}
	return nil
}

func (response Response) Validate() error {
	if response.Ops == nil {
		return errors.New("plugin response requires an ops array")
	}
	for index, operation := range response.Ops {
		if err := operation.Validate(); err != nil {
			return fmt.Errorf("operation %d: %w", index, err)
		}
	}
	return nil
}

func (operation Operation) Validate() error {
	switch operation.Op {
	case OperationFieldSet:
		if (operation.Entity != "item" && operation.Entity != "feed") || operation.EntityID == "" || operation.Name == "" || len(operation.Value) == 0 {
			return errors.New("field.set requires entity, entityId, name, and value")
		}
		if operation.ItemID != "" || operation.Role != "" || operation.URL != "" {
			return errors.New("field.set contains unrelated fields")
		}
	case OperationFieldDelete:
		if (operation.Entity != "item" && operation.Entity != "feed") || operation.EntityID == "" || operation.Name == "" {
			return errors.New("field.delete requires entity, entityId, and name")
		}
		if len(operation.Value) != 0 || operation.ItemID != "" || operation.Role != "" || operation.URL != "" {
			return errors.New("field.delete contains unrelated fields")
		}
	case OperationAssetAttach:
		if operation.ItemID == "" || operation.Role == "" || operation.URL == "" {
			return errors.New("asset.attach requires itemId, role, and url")
		}
		if operation.Entity != "" || operation.EntityID != "" || operation.Name != "" || len(operation.Value) != 0 {
			return errors.New("asset.attach contains unrelated fields")
		}
		parsedURL, err := url.ParseRequestURI(operation.URL)
		if err != nil || !safehttp.ValidURL(parsedURL) {
			return errors.New("asset.attach url must be an HTTP(S) URL")
		}
	case OperationAssetDetach:
		if operation.ItemID == "" || operation.Role == "" {
			return errors.New("asset.detach requires itemId and role")
		}
		if operation.Entity != "" || operation.EntityID != "" || operation.Name != "" || len(operation.Value) != 0 || operation.URL != "" {
			return errors.New("asset.detach contains unrelated fields")
		}
	case OperationItemMarkRead, OperationItemMarkUnread, OperationItemStar, OperationItemUnstar:
		if operation.ItemID == "" {
			return fmt.Errorf("%s requires itemId", operation.Op)
		}
		if operation.Entity != "" || operation.EntityID != "" || operation.Name != "" || len(operation.Value) != 0 || operation.Role != "" || operation.URL != "" {
			return fmt.Errorf("%s contains unrelated fields", operation.Op)
		}
	default:
		return fmt.Errorf("unsupported plugin operation %q", operation.Op)
	}
	return nil
}
