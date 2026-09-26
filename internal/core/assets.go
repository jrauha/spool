package core

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var ErrAssetNotFound = errors.New("asset not found")

func (s *PostgresStore) FindItemImageInputs(ctx context.Context, itemID string) (ItemImageInputs, error) {
	var inputs ItemImageInputs
	err := s.db.QueryRowContext(ctx, `
		SELECT url, image_url FROM items WHERE id = $1
	`, itemID).Scan(&inputs.PageURL, &inputs.ImageURL)
	if errors.Is(err, sql.ErrNoRows) {
		return ItemImageInputs{}, ErrItemNotFound
	}
	return inputs, err
}

// ReplaceItemAssetIfInputsMatch writes a new asset and attaches it to an item
// only while the item's image inputs still match the worker's request.
func (s *PostgresStore) ReplaceItemAssetIfInputsMatch(
	ctx context.Context,
	itemID string,
	inputs ItemImageInputs,
	role string,
	asset *Asset,
) (string, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()

	var current ItemImageInputs
	err = tx.QueryRowContext(ctx, `
		SELECT url, image_url FROM items WHERE id = $1 FOR UPDATE
	`, itemID).Scan(&current.PageURL, &current.ImageURL)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if current != inputs {
		return "", false, nil
	}

	var previousID string
	err = tx.QueryRowContext(ctx, `
		SELECT asset_id::text FROM item_assets WHERE item_id = $1 AND role = $2 FOR UPDATE
	`, itemID, role).Scan(&previousID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", false, err
	}

	if asset == nil {
		_, err = tx.ExecContext(ctx, `
			DELETE FROM item_assets WHERE item_id = $1 AND role = $2
		`, itemID, role)
	} else {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO assets (id, media_type, byte_size, sha256)
			VALUES ($1, $2, $3, $4)
		`, asset.ID, asset.MediaType, asset.ByteSize, asset.SHA256)
		if err == nil {
			_, err = tx.ExecContext(ctx, `
				INSERT INTO item_assets (item_id, role, asset_id)
				VALUES ($1, $2, $3)
				ON CONFLICT (item_id, role) DO UPDATE
				SET asset_id = EXCLUDED.asset_id, created_at = now()
			`, itemID, role, asset.ID)
		}
	}
	if err != nil {
		return "", false, err
	}
	if err := tx.Commit(); err != nil {
		return "", false, err
	}
	return previousID, true, nil
}

func (s *PostgresStore) FindAssetForUser(ctx context.Context, userID, assetID string) (Asset, error) {
	var asset Asset
	err := s.db.QueryRowContext(ctx, `
		SELECT assets.id::text, assets.media_type, assets.byte_size, assets.sha256, assets.created_at
		FROM assets
		JOIN item_assets ON item_assets.asset_id = assets.id
		JOIN items ON items.id = item_assets.item_id
		JOIN subscriptions ON subscriptions.feed_id = items.feed_id AND subscriptions.user_id = $1
		WHERE assets.id = $2
		LIMIT 1
	`, userID, assetID).Scan(&asset.ID, &asset.MediaType, &asset.ByteSize, &asset.SHA256, &asset.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Asset{}, ErrAssetNotFound
	}
	return asset, err
}

func (s *PostgresStore) ListAssets(ctx context.Context) ([]Asset, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id::text, media_type, byte_size, sha256, created_at FROM assets
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	assets := make([]Asset, 0)
	for rows.Next() {
		var asset Asset
		if err := rows.Scan(&asset.ID, &asset.MediaType, &asset.ByteSize, &asset.SHA256, &asset.CreatedAt); err != nil {
			return nil, err
		}
		assets = append(assets, asset)
	}
	return assets, rows.Err()
}

func (s *PostgresStore) DeleteOrphanAsset(ctx context.Context, assetID string, before time.Time) (bool, error) {
	var deletedID string
	err := s.db.QueryRowContext(ctx, `
		DELETE FROM assets
		WHERE id = $1 AND created_at < $2
			AND NOT EXISTS (SELECT 1 FROM item_assets WHERE item_assets.asset_id = assets.id)
		RETURNING id::text
	`, assetID, before).Scan(&deletedID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
