package thumbnail

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/spool-reader/spool/internal/asset"
	"github.com/spool-reader/spool/internal/core"
	"github.com/spool-reader/spool/internal/safehttp"
)

const (
	thumbnailRequestTimeout = 20 * time.Second
	maxPageBytes            = 2 << 20
	maxImageBytes           = 5 << 20
	maxImageDimension       = 8192
	maxImagePixels          = 20_000_000
	thumbnailUserAgent      = "Spool thumbnail fetcher"
)

type Store interface {
	FindItemImageInputs(ctx context.Context, itemID string) (core.ItemImageInputs, error)
	ReplaceItemAssetIfInputsMatch(ctx context.Context, itemID string, inputs core.ItemImageInputs, role string, asset *core.Asset) (string, bool, error)
}

type assetStorage interface {
	Save(data []byte, mediaType string) (core.Asset, error)
	RemoveFile(id string) error
}

type Service struct {
	store  Store
	assets assetStorage
	client *http.Client
}

func NewService(store Store, assets assetStorage, client *http.Client) *Service {
	if client == nil {
		client = safehttp.NewClient(thumbnailRequestTimeout)
	}
	return &Service{store: store, assets: assets, client: client}
}

func (s *Service) Process(ctx context.Context, args JobArgs) error {
	if args.ItemID == "" {
		return permanentThumbnailError{err: errors.New("invalid thumbnail job arguments")}
	}
	inputs := core.ItemImageInputs{PageURL: args.PageURL, ImageURL: args.ImageURL}
	current, err := s.store.FindItemImageInputs(ctx, args.ItemID)
	if err != nil {
		if errors.Is(err, core.ErrItemNotFound) {
			return nil
		}
		return err
	}
	if current != inputs {
		return nil
	}

	imageURL := args.ImageURL
	if imageURL != "" {
		if !validImageURL(imageURL) {
			return s.markMissing(ctx, args.ItemID, inputs)
		}
	} else {
		if !validImageURL(args.PageURL) {
			return s.markMissing(ctx, args.ItemID, inputs)
		}
		page, pageFinalURL, err := s.fetchPage(ctx, args.PageURL)
		if err != nil {
			var permanent permanentThumbnailError
			if errors.As(err, &permanent) {
				return s.markMissing(ctx, args.ItemID, inputs)
			}
			return err
		}
		imageURL = extractImageURL(pageFinalURL, string(page))
		if imageURL == "" {
			return s.markMissing(ctx, args.ItemID, inputs)
		}
	}

	imageData, mimeType, err := s.fetchImage(ctx, imageURL)
	if err != nil {
		var permanent permanentThumbnailError
		if errors.As(err, &permanent) {
			return s.markMissing(ctx, args.ItemID, inputs)
		}
		return err
	}
	imageData, mimeType, err = transformImage(imageData, mimeType)
	if err != nil {
		return fmt.Errorf("transform thumbnail image: %w", err)
	}
	asset, err := s.assets.Save(imageData, mimeType)
	if err != nil {
		return err
	}

	_, updated, err := s.store.ReplaceItemAssetIfInputsMatch(ctx, args.ItemID, inputs, core.ItemAssetRoleThumbnail, &asset)
	if err != nil {
		return err
	}
	if !updated {
		_ = s.assets.RemoveFile(asset.ID)
	}
	return nil
}

func (s *Service) markMissing(ctx context.Context, itemID string, inputs core.ItemImageInputs) error {
	_, _, err := s.store.ReplaceItemAssetIfInputsMatch(ctx, itemID, inputs, core.ItemAssetRoleThumbnail, nil)
	return err
}

func validImageURL(rawURL string) bool {
	parsedURL, err := url.ParseRequestURI(rawURL)
	return err == nil && safehttp.ValidURL(parsedURL)
}

func (s *Service) fetchPage(ctx context.Context, pageURL string) ([]byte, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, "", permanentThumbnailError{err: err}
	}
	request.Header.Set("User-Agent", thumbnailUserAgent)
	response, err := s.client.Do(request)
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusBadRequest && response.StatusCode < http.StatusInternalServerError && response.StatusCode != http.StatusRequestTimeout && response.StatusCode != http.StatusTooManyRequests {
		return nil, "", permanentThumbnailError{err: fmt.Errorf("item page returned %s", response.Status)}
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, "", fmt.Errorf("item page returned %s", response.Status)
	}
	body, err := readLimited(response.Body, maxPageBytes)
	if err != nil {
		return nil, "", permanentThumbnailError{err: err}
	}
	finalURL := pageURL
	if response.Request != nil && response.Request.URL != nil {
		finalURL = response.Request.URL.String()
	}
	return body, finalURL, nil
}

func (s *Service) fetchImage(ctx context.Context, imageURL string) ([]byte, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return nil, "", permanentThumbnailError{err: err}
	}
	request.Header.Set("User-Agent", thumbnailUserAgent)
	response, err := s.client.Do(request)
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusBadRequest && response.StatusCode < http.StatusInternalServerError && response.StatusCode != http.StatusRequestTimeout && response.StatusCode != http.StatusTooManyRequests {
		return nil, "", permanentThumbnailError{err: fmt.Errorf("thumbnail image returned %s", response.Status)}
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, "", fmt.Errorf("thumbnail image returned %s", response.Status)
	}
	body, err := readLimited(response.Body, maxImageBytes)
	if err != nil {
		return nil, "", permanentThumbnailError{err: err}
	}
	mimeType, err := validateImage(body)
	if err != nil {
		return nil, "", permanentThumbnailError{err: err}
	}
	return body, mimeType, nil
}

func readLimited(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("response exceeds %d bytes", limit)
	}
	return data, nil
}

func validateImage(data []byte) (string, error) {
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("decode thumbnail image: %w", err)
	}
	if config.Width < 1 || config.Height < 1 || config.Width > maxImageDimension || config.Height > maxImageDimension || int64(config.Width)*int64(config.Height) > maxImagePixels {
		return "", errors.New("thumbnail image dimensions exceed limits")
	}
	if format != "gif" {
		if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
			return "", fmt.Errorf("decode thumbnail image: %w", err)
		}
	}
	switch format {
	case "jpeg":
		return asset.MediaTypeJPEG, nil
	case "png":
		return asset.MediaTypePNG, nil
	case "gif":
		return asset.MediaTypeGIF, nil
	default:
		return "", fmt.Errorf("unsupported thumbnail image format %q", format)
	}
}

type permanentThumbnailError struct {
	err error
}

func (e permanentThumbnailError) Error() string   { return e.err.Error() }
func (e permanentThumbnailError) Unwrap() error   { return e.err }
func (e permanentThumbnailError) Permanent() bool { return true }
