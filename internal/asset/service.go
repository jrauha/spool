package asset

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spool-reader/spool/internal/core"
)

const (
	fileMode          = 0600
	temporaryPrefix   = ".asset-"
	orphanGracePeriod = time.Hour

	MediaTypeJPEG = "image/jpeg"
	MediaTypePNG  = "image/png"
	MediaTypeGIF  = "image/gif"
)

type Store interface {
	FindAssetForUser(ctx context.Context, userID, assetID string) (core.Asset, error)
	ListAssets(ctx context.Context) ([]core.Asset, error)
	DeleteOrphanAsset(ctx context.Context, assetID string, before time.Time) (bool, error)
}

type Service struct {
	store Store
	dir   string
}

func NewService(store Store, dir string) *Service {
	return &Service{store: store, dir: dir}
}

func (s *Service) Save(data []byte, mediaType string) (core.Asset, error) {
	id, err := newID()
	if err != nil {
		return core.Asset{}, err
	}
	digest := sha256.Sum256(data)
	asset := core.Asset{
		ID:        id,
		MediaType: mediaType,
		ByteSize:  int64(len(data)),
		SHA256:    hex.EncodeToString(digest[:]),
	}
	if err := s.writeFile(id, data); err != nil {
		return core.Asset{}, err
	}
	return asset, nil
}

func (s *Service) RemoveFile(id string) error {
	if !validID(id) {
		return core.ErrAssetNotFound
	}
	return removeIfExists(s.assetPath(id))
}

func (s *Service) OpenImageForUser(ctx context.Context, userID, id string) (*os.File, string, error) {
	if !validID(id) {
		return nil, "", core.ErrAssetNotFound
	}
	asset, err := s.store.FindAssetForUser(ctx, userID, id)
	if err != nil {
		return nil, "", err
	}
	if !supportedImageMediaType(asset.MediaType) {
		return nil, "", core.ErrAssetNotFound
	}
	file, err := os.Open(s.assetPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil, "", core.ErrAssetNotFound
	}
	return file, asset.MediaType, err
}

func (s *Service) Cleanup(ctx context.Context) error {
	assets, err := s.store.ListAssets(ctx)
	if err != nil {
		return err
	}
	known := make(map[string]struct{}, len(assets))
	cutoff := time.Now().Add(-orphanGracePeriod)
	for _, asset := range assets {
		known[asset.ID] = struct{}{}
		if asset.CreatedAt.After(cutoff) {
			continue
		}
		deleted, err := s.store.DeleteOrphanAsset(ctx, asset.ID, cutoff)
		if err != nil {
			return err
		}
		if deleted {
			delete(known, asset.ID)
			if err := removeIfExists(s.assetPath(asset.ID)); err != nil {
				return err
			}
		}
	}

	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || (!strings.HasPrefix(entry.Name(), temporaryPrefix) && !validID(entry.Name())) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.ModTime().After(cutoff) {
			continue
		}
		if _, ok := known[entry.Name()]; ok {
			continue
		}
		if err := removeIfExists(filepath.Join(s.dir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) writeFile(id string, data []byte) error {
	if err := os.MkdirAll(s.dir, 0750); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(s.dir, temporaryPrefix+"*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(fileMode); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryName, s.assetPath(id))
}

func (s *Service) assetPath(id string) string {
	return filepath.Join(s.dir, id)
}

func newID() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	random[6] = (random[6] & 0x0f) | 0x40
	random[8] = (random[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", random[0:4], random[4:6], random[6:8], random[8:10], random[10:16]), nil
}

func validID(id string) bool {
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' || strings.ToLower(id) != id {
		return false
	}
	_, err := hex.DecodeString(strings.ReplaceAll(id, "-", ""))
	return err == nil
}

func supportedImageMediaType(mediaType string) bool {
	switch mediaType {
	case MediaTypeJPEG, MediaTypePNG, MediaTypeGIF:
		return true
	default:
		return false
	}
}

func removeIfExists(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
