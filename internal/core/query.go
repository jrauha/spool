package core

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	itemquery "github.com/spool-reader/spool/internal/query"
)

type itemFieldKind int

const (
	itemFieldID itemFieldKind = iota
	itemFieldString
	itemFieldTime
	itemFieldRead
	itemFieldSearch
)

type itemField struct {
	column    string
	kind      itemFieldKind
	nullable  bool
	wildcards bool
}

var itemFields = map[string]itemField{
	"id":          {column: "id", kind: itemFieldID},
	"feed.id":     {column: "feed_id", kind: itemFieldID},
	"feed.title":  {column: "feed_title", kind: itemFieldString, wildcards: true},
	"title":       {column: "title", kind: itemFieldString, wildcards: true},
	"author":      {column: "author", kind: itemFieldString, wildcards: true},
	"url":         {column: "url", kind: itemFieldString},
	"publishedAt": {column: "published_at", kind: itemFieldTime, nullable: true},
	"createdAt":   {column: "created_at", kind: itemFieldTime},
	"date":        {column: "sort_at", kind: itemFieldTime},
	"read":        {column: "read_at", kind: itemFieldRead},
	"text":        {column: "search_vector", kind: itemFieldSearch},
}

type itemFilterCompiler struct {
	args        []any
	searchRanks []string
}

func (s *PostgresStore) QueryItemsForUser(ctx context.Context, userID string, query ItemQuery) ([]ItemMatch, error) {
	if query.Limit <= 0 {
		return []ItemMatch{}, nil
	}

	compiler := &itemFilterCompiler{args: []any{userID}}
	filterSQL, err := compiler.compileExpression(query.Filter)
	if err != nil {
		return nil, err
	}
	rankSQL := "0::real"
	if len(compiler.searchRanks) == 1 {
		rankSQL = compiler.searchRanks[0]
	} else if len(compiler.searchRanks) > 1 {
		rankSQL = "GREATEST(" + strings.Join(compiler.searchRanks, ", ") + ")"
	}

	cursorSQL, orderSQL, err := compiler.pagination(query)
	if err != nil {
		return nil, err
	}
	limitPlaceholder := compiler.bind(query.Limit)

	statement := fmt.Sprintf(`
		WITH user_items AS (
			SELECT items.id, items.feed_id, items.guid, items.url, items.image_url, items.title,
				items.summary, items.author, items.published_at,
				CASE
					WHEN read_override.is_read THEN read_override.read_at
					WHEN read_override.is_read = false THEN NULL
					WHEN subscriptions.read_before IS NOT NULL AND items.created_at <= subscriptions.read_before THEN subscriptions.read_before
					ELSE NULL
				END AS read_at,
				items.created_at, items.updated_at, feeds.title AS feed_title,
				coalesce(items.published_at, items.created_at) AS sort_at,
				items.search_vector
			FROM subscriptions
			JOIN feeds ON feeds.id = subscriptions.feed_id
			JOIN items ON items.feed_id = subscriptions.feed_id
			LEFT JOIN item_read_overrides read_override
				ON read_override.item_id = items.id AND read_override.user_id = subscriptions.user_id
			WHERE subscriptions.user_id = $1
		), matched_items AS (
			SELECT *, %s AS search_rank
			FROM user_items
			WHERE %s
		)
		SELECT id::text, feed_id::text, guid, url, image_url, title, summary, author,
			published_at, read_at, created_at, updated_at, feed_title, sort_at,
			search_rank, item_assets.attachments
		FROM matched_items
		LEFT JOIN LATERAL (
			SELECT COALESCE(json_agg(json_build_object(
				'assetId', item_assets.asset_id::text,
				'role', item_assets.role,
				'mediaType', assets.media_type
			) ORDER BY item_assets.role), '[]'::json) AS attachments
			FROM item_assets
			JOIN assets ON assets.id = item_assets.asset_id
			WHERE item_assets.item_id = matched_items.id
		) AS item_assets ON true
		WHERE %s
		ORDER BY %s
		LIMIT %s
	`, rankSQL, filterSQL, cursorSQL, orderSQL, limitPlaceholder)

	rows, err := s.db.QueryContext(ctx, statement, compiler.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	matches := make([]ItemMatch, 0, query.Limit)
	for rows.Next() {
		var match ItemMatch
		var attachments []byte
		if err := rows.Scan(
			&match.ID,
			&match.FeedID,
			&match.GUID,
			&match.URL,
			&match.ImageURL,
			&match.Title,
			&match.Summary,
			&match.Author,
			&match.PublishedAt,
			&match.ReadAt,
			&match.CreatedAt,
			&match.UpdatedAt,
			&match.FeedTitle,
			&match.SortAt,
			&match.Rank,
			&attachments,
		); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(attachments, &match.Assets); err != nil {
			return nil, fmt.Errorf("decode item assets: %w", err)
		}
		matches = append(matches, match)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if query.Cursor != nil && query.Cursor.Before {
		slices.Reverse(matches)
	}
	return matches, nil
}

func (c *itemFilterCompiler) compileExpression(expression *itemquery.Expression) (string, error) {
	if expression == nil {
		return "TRUE", nil
	}
	parts := make([]string, 0, len(expression.Conjunctions))
	for _, conjunction := range expression.Conjunctions {
		part, err := c.compileConjunction(conjunction)
		if err != nil {
			return "", err
		}
		parts = append(parts, part)
	}
	return grouped(parts, " OR "), nil
}

func (c *itemFilterCompiler) compileConjunction(conjunction *itemquery.Conjunction) (string, error) {
	parts := make([]string, 0, len(conjunction.Terms))
	for _, term := range conjunction.Terms {
		var (
			part string
			err  error
		)
		if term.Comparison != nil {
			part, err = c.compileComparison(term.Comparison)
		} else {
			part, err = c.compileExpression(term.Nested)
		}
		if err != nil {
			return "", err
		}
		parts = append(parts, part)
	}
	return grouped(parts, " AND "), nil
}

func (c *itemFilterCompiler) compileComparison(comparison *itemquery.Comparison) (string, error) {
	field, ok := itemFields[comparison.Selector]
	if !ok {
		return "", invalidItemQuery("selector %q is not supported", comparison.Selector)
	}
	values := comparison.Values()
	if field.kind == itemFieldSearch {
		return c.compileSearch(field, comparison.Operator, values)
	}
	if comparison.Operator == itemquery.OperatorIn || comparison.Operator == itemquery.OperatorOut {
		return c.compileList(field, comparison.Operator, values)
	}
	if len(values) != 1 {
		return "", invalidItemQuery("operator %q requires one value", comparison.Operator)
	}
	return c.compileScalar(field, comparison.Operator, values[0])
}

func (c *itemFilterCompiler) compileScalar(field itemField, operator itemquery.Operator, value string) (string, error) {
	if field.nullable && strings.EqualFold(value, "null") {
		switch operator {
		case itemquery.OperatorEqual:
			return field.column + " IS NULL", nil
		case itemquery.OperatorNotEqual:
			return field.column + " IS NOT NULL", nil
		default:
			return "", invalidItemQuery("operator %q cannot compare null", operator)
		}
	}

	switch field.kind {
	case itemFieldID:
		if !equalityOperator(operator) {
			return "", invalidItemQuery("operator %q is not supported for IDs", operator)
		}
		if err := validateUUID(value); err != nil {
			return "", err
		}
		return field.column + " " + sqlOperator(operator) + " " + c.bind(value), nil
	case itemFieldString:
		if !equalityOperator(operator) {
			return "", invalidItemQuery("operator %q is not supported for strings", operator)
		}
		if field.wildcards && strings.Contains(value, "*") {
			comparison := "ILIKE"
			if operator == itemquery.OperatorNotEqual {
				comparison = "NOT ILIKE"
			}
			return field.column + " " + comparison + " " + c.bind(wildcardPattern(value)) + ` ESCAPE '\'`, nil
		}
		return field.column + " " + sqlOperator(operator) + " " + c.bind(value), nil
	case itemFieldTime:
		if !orderedOperator(operator) {
			return "", invalidItemQuery("operator %q is not supported for timestamps", operator)
		}
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return "", invalidItemQuery("%q is not an RFC3339 timestamp", value)
		}
		return field.column + " " + sqlOperator(operator) + " " + c.bind(parsed), nil
	case itemFieldRead:
		if !equalityOperator(operator) {
			return "", invalidItemQuery("operator %q is not supported for read state", operator)
		}
		if value != "true" && value != "false" {
			return "", invalidItemQuery("read state must be true or false")
		}
		read, _ := strconv.ParseBool(value)
		if operator == itemquery.OperatorNotEqual {
			read = !read
		}
		if read {
			return field.column + " IS NOT NULL", nil
		}
		return field.column + " IS NULL", nil
	default:
		return "", invalidItemQuery("unsupported selector")
	}
}

func (c *itemFilterCompiler) compileList(field itemField, operator itemquery.Operator, values []string) (string, error) {
	if field.kind != itemFieldID && field.kind != itemFieldString {
		return "", invalidItemQuery("operator %q is not supported for this selector", operator)
	}
	placeholders := make([]string, 0, len(values))
	for _, value := range values {
		if field.kind == itemFieldID {
			if err := validateUUID(value); err != nil {
				return "", err
			}
		}
		placeholders = append(placeholders, c.bind(value))
	}
	keyword := "IN"
	if operator == itemquery.OperatorOut {
		keyword = "NOT IN"
	}
	return field.column + " " + keyword + " (" + strings.Join(placeholders, ", ") + ")", nil
}

func (c *itemFilterCompiler) compileSearch(field itemField, operator itemquery.Operator, values []string) (string, error) {
	if operator != itemquery.OperatorSearch || len(values) != 1 {
		return "", invalidItemQuery("text only supports =search=")
	}
	text := strings.TrimSpace(values[0])
	if text == "" {
		return "", invalidItemQuery("search text cannot be empty")
	}
	if len(text) > itemquery.MaxSearchTextBytes {
		return "", invalidItemQuery("search text exceeds %d bytes", itemquery.MaxSearchTextBytes)
	}
	querySQL := "websearch_to_tsquery('simple', " + c.bind(text) + ")"
	c.searchRanks = append(c.searchRanks, "ts_rank_cd("+field.column+", "+querySQL+")")
	return field.column + " @@ " + querySQL, nil
}

func (c *itemFilterCompiler) pagination(query ItemQuery) (string, string, error) {
	if query.Cursor != nil {
		if err := validateUUID(query.Cursor.ItemID); err != nil {
			return "", "", err
		}
	}
	switch query.Sort {
	case ItemQuerySortNewest:
		if query.Cursor == nil {
			return "TRUE", "sort_at DESC, id DESC", nil
		}
		date := c.bind(query.Cursor.SortAt)
		id := c.bind(query.Cursor.ItemID)
		if query.Cursor.Before {
			return "(sort_at, id) > (" + date + ", " + id + ")", "sort_at ASC, id ASC", nil
		}
		return "(sort_at, id) < (" + date + ", " + id + ")", "sort_at DESC, id DESC", nil
	case ItemQuerySortOldest:
		if query.Cursor == nil {
			return "TRUE", "sort_at ASC, id ASC", nil
		}
		date := c.bind(query.Cursor.SortAt)
		id := c.bind(query.Cursor.ItemID)
		if query.Cursor.Before {
			return "(sort_at, id) < (" + date + ", " + id + ")", "sort_at DESC, id DESC", nil
		}
		return "(sort_at, id) > (" + date + ", " + id + ")", "sort_at ASC, id ASC", nil
	case ItemQuerySortRelevance:
		if len(c.searchRanks) == 0 {
			return "", "", invalidItemQuery("relevance sort requires a text search")
		}
		if query.Cursor == nil {
			return "TRUE", "search_rank DESC, sort_at DESC, id DESC", nil
		}
		rank := c.bind(query.Cursor.Rank)
		date := c.bind(query.Cursor.SortAt)
		id := c.bind(query.Cursor.ItemID)
		if query.Cursor.Before {
			cursor := "(search_rank > " + rank + " OR (search_rank = " + rank + " AND (sort_at, id) > (" + date + ", " + id + ")))"
			return cursor, "search_rank ASC, sort_at ASC, id ASC", nil
		}
		cursor := "(search_rank < " + rank + " OR (search_rank = " + rank + " AND (sort_at, id) < (" + date + ", " + id + ")))"
		return cursor, "search_rank DESC, sort_at DESC, id DESC", nil
	default:
		return "", "", invalidItemQuery("sort %q is not supported", query.Sort)
	}
}

func (c *itemFilterCompiler) bind(value any) string {
	c.args = append(c.args, value)
	return fmt.Sprintf("$%d", len(c.args))
}

func equalityOperator(operator itemquery.Operator) bool {
	return operator == itemquery.OperatorEqual || operator == itemquery.OperatorNotEqual
}

func orderedOperator(operator itemquery.Operator) bool {
	return equalityOperator(operator) || operator == itemquery.OperatorGreater || operator == itemquery.OperatorGreaterEqual ||
		operator == itemquery.OperatorLess || operator == itemquery.OperatorLessEqual
}

func sqlOperator(operator itemquery.Operator) string {
	switch operator {
	case itemquery.OperatorEqual:
		return "="
	case itemquery.OperatorNotEqual:
		return "<>"
	case itemquery.OperatorGreater:
		return ">"
	case itemquery.OperatorGreaterEqual:
		return ">="
	case itemquery.OperatorLess:
		return "<"
	case itemquery.OperatorLessEqual:
		return "<="
	default:
		panic("unsupported SQL operator")
	}
}

func wildcardPattern(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`, `*`, `%`)
	return replacer.Replace(value)
}

func grouped(parts []string, separator string) string {
	return "(" + strings.Join(parts, separator) + ")"
}

func validateUUID(value string) error {
	var id pgtype.UUID
	if err := id.Scan(value); err != nil || !id.Valid {
		return invalidItemQuery("%q is not a UUID", value)
	}
	return nil
}

func invalidItemQuery(format string, args ...any) error {
	return fmt.Errorf("%w: %s", itemquery.ErrInvalid, fmt.Sprintf(format, args...))
}
