package feed

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spool-reader/spool/internal/core"
	itemquery "github.com/spool-reader/spool/internal/query"
)

const (
	defaultItemQueryLimit = 20
	maxItemQueryLimit     = 100
	itemCursorVersion     = 1
	maxItemCursorBytes    = 1 << 10
)

var ErrInvalidQuery = errors.New("invalid item query")

type ItemSort string

const (
	ItemSortAuto      ItemSort = "auto"
	ItemSortNewest    ItemSort = "-date"
	ItemSortOldest    ItemSort = "date"
	ItemSortRelevance ItemSort = "relevance"
)

type ItemQuery struct {
	Text   string
	Filter string
	Sort   ItemSort
	Limit  int
	Cursor string
}

type ItemPage struct {
	Items          []core.ItemMatch
	PreviousCursor string
	NextCursor     string
}

type itemCursor struct {
	Version     int       `json:"v"`
	Fingerprint string    `json:"f"`
	Sort        ItemSort  `json:"s"`
	SortAt      time.Time `json:"d"`
	ItemID      string    `json:"i"`
	Rank        float64   `json:"r,omitempty"`
	Before      bool      `json:"b,omitempty"`
}

func (s *Service) QueryItemsForUser(ctx context.Context, userID string, input ItemQuery) (ItemPage, error) {
	text := strings.TrimSpace(input.Text)
	filter := strings.TrimSpace(input.Filter)
	if text != "" && filter != "" {
		return ItemPage{}, invalidQuery("text and RSQL filter cannot be combined")
	}

	var (
		expression  *itemquery.Expression
		fingerprint string
		err         error
	)
	if text != "" {
		expression = textSearchExpression(text)
		fingerprint = "text\x00" + text
	} else {
		expression, err = itemquery.Parse(filter)
		if err != nil {
			return ItemPage{}, wrapInvalidQuery(err)
		}
		fingerprint = "rsql\x00" + filter
	}
	if err := validateItemFilter(expression); err != nil {
		return ItemPage{}, err
	}

	limit := input.Limit
	if limit == 0 {
		limit = defaultItemQueryLimit
	}
	if limit < 1 || limit > maxItemQueryLimit {
		return ItemPage{}, invalidQuery("limit must be between 1 and %d", maxItemQueryLimit)
	}
	sort, err := resolveItemSort(input.Sort, expression)
	if err != nil {
		return ItemPage{}, err
	}
	fingerprint = queryFingerprint(fingerprint, sort)
	cursor, err := decodeItemCursor(input.Cursor, fingerprint, sort)
	if err != nil {
		return ItemPage{}, err
	}

	matches, err := s.store.QueryItemsForUser(ctx, userID, core.ItemQuery{
		Filter: expression,
		Sort:   coreItemSort(sort),
		Limit:  limit + 1,
		Cursor: cursor,
	})
	if err != nil {
		if errors.Is(err, itemquery.ErrInvalid) {
			return ItemPage{}, wrapInvalidQuery(err)
		}
		return ItemPage{}, err
	}

	before := cursor != nil && cursor.Before
	hasMore := len(matches) > limit
	if hasMore {
		if before {
			matches = matches[1:]
		} else {
			matches = matches[:limit]
		}
	}
	page := ItemPage{Items: matches}
	if len(page.Items) == 0 {
		return page, nil
	}
	if (cursor != nil && !before) || (before && hasMore) {
		page.PreviousCursor, err = encodeItemCursor(page.Items[0], fingerprint, sort, true)
		if err != nil {
			return ItemPage{}, err
		}
	}
	if (!before && hasMore) || before {
		page.NextCursor, err = encodeItemCursor(page.Items[len(page.Items)-1], fingerprint, sort, false)
		if err != nil {
			return ItemPage{}, err
		}
	}
	return page, nil
}

func textSearchExpression(text string) *itemquery.Expression {
	return &itemquery.Expression{Conjunctions: []*itemquery.Conjunction{{Terms: []*itemquery.Term{{
		Comparison: &itemquery.Comparison{
			Selector: "text",
			Operator: itemquery.OperatorSearch,
			Scalar:   &text,
		},
	}}}}}
}

func validateItemFilter(expression *itemquery.Expression) error {
	if expression == nil {
		return nil
	}
	for _, conjunction := range expression.Conjunctions {
		for _, term := range conjunction.Terms {
			if term.Nested != nil {
				if err := validateItemFilter(term.Nested); err != nil {
					return err
				}
				continue
			}
			comparison := term.Comparison
			if comparison == nil || !itemOperatorAllowed(comparison.Selector, comparison.Operator) {
				selector := ""
				operator := itemquery.Operator("")
				if comparison != nil {
					selector = comparison.Selector
					operator = comparison.Operator
				}
				return invalidQuery("operator %q is not supported for selector %q", operator, selector)
			}
			if err := validateItemValues(comparison); err != nil {
				return err
			}
		}
	}
	return nil
}

func itemOperatorAllowed(selector string, operator itemquery.Operator) bool {
	equality := operator == itemquery.OperatorEqual || operator == itemquery.OperatorNotEqual
	membership := operator == itemquery.OperatorIn || operator == itemquery.OperatorOut
	ordered := operator == itemquery.OperatorGreater || operator == itemquery.OperatorGreaterEqual ||
		operator == itemquery.OperatorLess || operator == itemquery.OperatorLessEqual
	switch selector {
	case "id", "feed.id":
		return equality || membership
	case "feed.title", "title", "author", "url":
		return equality || membership
	case "publishedAt", "createdAt", "date":
		return equality || ordered
	case "read":
		return equality
	case "text":
		return operator == itemquery.OperatorSearch
	default:
		return false
	}
}

func validateItemValues(comparison *itemquery.Comparison) error {
	values := comparison.Values()
	switch comparison.Selector {
	case "id", "feed.id":
		for _, value := range values {
			if !validQueryUUID(value) {
				return invalidQuery("%q is not a UUID", value)
			}
		}
	case "publishedAt", "createdAt", "date":
		for _, value := range values {
			if comparison.Selector == "publishedAt" && strings.EqualFold(value, "null") &&
				(comparison.Operator == itemquery.OperatorEqual || comparison.Operator == itemquery.OperatorNotEqual) {
				continue
			}
			if _, err := time.Parse(time.RFC3339, value); err != nil {
				return invalidQuery("%q is not an RFC3339 timestamp", value)
			}
		}
	case "read":
		if len(values) != 1 || (values[0] != "true" && values[0] != "false") {
			return invalidQuery("read state must be true or false")
		}
	case "text":
		if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
			return invalidQuery("search text cannot be empty")
		}
		if len(values[0]) > itemquery.MaxSearchTextBytes {
			return invalidQuery("search text exceeds %d bytes", itemquery.MaxSearchTextBytes)
		}
	}
	return nil
}

func validQueryUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	_, err := hex.DecodeString(strings.ReplaceAll(value, "-", ""))
	return err == nil
}

func resolveItemSort(sort ItemSort, expression *itemquery.Expression) (ItemSort, error) {
	hasSearch := expression.HasComparison("text", itemquery.OperatorSearch)
	if sort == "" || sort == ItemSortAuto {
		if hasSearch {
			return ItemSortRelevance, nil
		}
		return ItemSortNewest, nil
	}
	switch sort {
	case ItemSortNewest, ItemSortOldest:
		return sort, nil
	case ItemSortRelevance:
		if !hasSearch {
			return "", invalidQuery("relevance sort requires text=search=...")
		}
		return sort, nil
	default:
		return "", invalidQuery("sort %q is not supported", sort)
	}
}

func coreItemSort(sort ItemSort) core.ItemQuerySort {
	switch sort {
	case ItemSortOldest:
		return core.ItemQuerySortOldest
	case ItemSortRelevance:
		return core.ItemQuerySortRelevance
	default:
		return core.ItemQuerySortNewest
	}
}

func queryFingerprint(filter string, sort ItemSort) string {
	sum := sha256.Sum256([]byte(filter + "\x00" + string(sort)))
	return hex.EncodeToString(sum[:])
}

func encodeItemCursor(match core.ItemMatch, fingerprint string, sort ItemSort, before bool) (string, error) {
	payload, err := json.Marshal(itemCursor{
		Version:     itemCursorVersion,
		Fingerprint: fingerprint,
		Sort:        sort,
		SortAt:      match.SortAt,
		ItemID:      match.ID,
		Rank:        match.Rank,
		Before:      before,
	})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeItemCursor(raw, fingerprint string, sort ItemSort) (*core.ItemQueryCursor, error) {
	if raw == "" {
		return nil, nil
	}
	if len(raw) > maxItemCursorBytes {
		return nil, invalidQuery("cursor is too long")
	}
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, invalidQuery("cursor is malformed")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var cursor itemCursor
	if err := decoder.Decode(&cursor); err != nil {
		return nil, invalidQuery("cursor is malformed")
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return nil, invalidQuery("cursor is malformed")
	}
	if cursor.Version != itemCursorVersion || cursor.Fingerprint != fingerprint || cursor.Sort != sort || cursor.SortAt.IsZero() || cursor.ItemID == "" {
		return nil, invalidQuery("cursor does not match the query")
	}
	return &core.ItemQueryCursor{SortAt: cursor.SortAt, ItemID: cursor.ItemID, Rank: cursor.Rank, Before: cursor.Before}, nil
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("trailing cursor data")
	}
	return nil
}

func invalidQuery(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidQuery, fmt.Sprintf(format, args...))
}

func wrapInvalidQuery(err error) error {
	return fmt.Errorf("%w: %v", ErrInvalidQuery, err)
}
