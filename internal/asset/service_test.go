package asset

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spool-reader/spool/internal/core"
)

func TestSaveAndOpenImageForUser(t *testing.T) {
	store := newAssetStore()
	directory := t.TempDir()
	service := NewService(store, directory)
	data := []byte("cached image")

	asset, err := service.Save(data, MediaTypePNG)
	if err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	if !validID(asset.ID) || asset.MediaType != MediaTypePNG || asset.ByteSize != int64(len(data)) {
		t.Fatalf("saved asset metadata = %#v", asset)
	}
	digest := sha256.Sum256(data)
	if asset.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("asset checksum = %q", asset.SHA256)
	}
	stored, err := os.ReadFile(filepath.Join(directory, asset.ID))
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if string(stored) != string(data) {
		t.Fatalf("stored asset = %q, want %q", stored, data)
	}

	store.assets[asset.ID] = asset
	store.attached[asset.ID] = true
	file, mediaType, err := service.OpenImageForUser(context.Background(), "user-1", asset.ID)
	if err != nil {
		t.Fatalf("OpenImageForUser returned error: %v", err)
	}
	defer file.Close()
	if mediaType != MediaTypePNG {
		t.Fatalf("media type = %q, want %q", mediaType, MediaTypePNG)
	}
}

func TestOpenImageForUserRejectsUnattachedAndNonImageAssets(t *testing.T) {
	store := newAssetStore()
	service := NewService(store, t.TempDir())
	id := "12345678-1234-1234-1234-123456789abc"
	store.assets[id] = core.Asset{ID: id, MediaType: "text/html"}

	if _, _, err := service.OpenImageForUser(context.Background(), "user-1", id); !errors.Is(err, core.ErrAssetNotFound) {
		t.Fatalf("unattached asset error = %v, want ErrAssetNotFound", err)
	}
	store.attached[id] = true
	if _, _, err := service.OpenImageForUser(context.Background(), "user-1", id); !errors.Is(err, core.ErrAssetNotFound) {
		t.Fatalf("non-image asset error = %v, want ErrAssetNotFound", err)
	}
	if _, _, err := service.OpenImageForUser(context.Background(), "user-1", "../secret"); !errors.Is(err, core.ErrAssetNotFound) {
		t.Fatalf("invalid ID error = %v, want ErrAssetNotFound", err)
	}
}

func TestCleanupRemovesOldOrphanFilesAndAssets(t *testing.T) {
	const attachedID = "12345678-1234-1234-1234-123456789abc"
	const orphanID = "abcdef01-2345-6789-abcd-ef0123456789"
	store := newAssetStore()
	old := time.Now().Add(-2 * orphanGracePeriod)
	store.assets[attachedID] = core.Asset{ID: attachedID, CreatedAt: old}
	store.assets[orphanID] = core.Asset{ID: orphanID, CreatedAt: old}
	store.attached[attachedID] = true
	directory := t.TempDir()
	keptPath := filepath.Join(directory, attachedID)
	orphanPath := filepath.Join(directory, orphanID)
	for _, path := range []string{keptPath, orphanPath} {
		if err := os.WriteFile(path, []byte("image"), 0600); err != nil {
			t.Fatalf("WriteFile returned error: %v", err)
		}
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatalf("Chtimes returned error: %v", err)
		}
	}

	service := NewService(store, directory)
	if err := service.Cleanup(context.Background()); err != nil {
		t.Fatalf("Cleanup returned error: %v", err)
	}
	if _, err := os.Stat(keptPath); err != nil {
		t.Fatalf("Cleanup removed referenced asset: %v", err)
	}
	if _, err := os.Stat(orphanPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphan file still exists or stat failed: %v", err)
	}
	if _, ok := store.assets[orphanID]; ok {
		t.Fatal("cleanup left orphan asset metadata")
	}
}

type assetStore struct {
	assets   map[string]core.Asset
	attached map[string]bool
}

func newAssetStore() *assetStore {
	return &assetStore{assets: make(map[string]core.Asset), attached: make(map[string]bool)}
}

func (s *assetStore) FindAssetForUser(_ context.Context, _, id string) (core.Asset, error) {
	asset, exists := s.assets[id]
	if !exists || !s.attached[id] {
		return core.Asset{}, core.ErrAssetNotFound
	}
	return asset, nil
}

func (s *assetStore) ListAssets(context.Context) ([]core.Asset, error) {
	assets := make([]core.Asset, 0, len(s.assets))
	for _, asset := range s.assets {
		assets = append(assets, asset)
	}
	return assets, nil
}

func (s *assetStore) DeleteOrphanAsset(_ context.Context, id string, before time.Time) (bool, error) {
	asset, exists := s.assets[id]
	if !exists || asset.CreatedAt.After(before) || s.attached[id] {
		return false, nil
	}
	delete(s.assets, id)
	return true, nil
}
