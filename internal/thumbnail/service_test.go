package thumbnail

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spool-reader/spool/internal/asset"
	"github.com/spool-reader/spool/internal/core"
)

func TestProcessCachesValidatedImage(t *testing.T) {
	imageBytes := testPNG(t, 2, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/article":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<meta name="twitter:image" content="/image.png">`))
		case "/image.png":
			_, _ = w.Write(imageBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	pageURL := "https://example.com/article"
	client := testHTTPClient(t, server)
	store := newThumbnailStore(core.ItemImageInputs{PageURL: pageURL})
	directory := t.TempDir()
	assetService := asset.NewService(store, directory)
	service := NewService(store, assetService, client)
	if err := service.Process(context.Background(), JobArgs{ItemID: "item-1", PageURL: pageURL}); err != nil {
		t.Fatalf("Process returned error: %v", err)
	}
	if store.currentID == "" || store.assets[store.currentID].MediaType != "image/png" {
		t.Fatalf("attached asset = %#v", store.assets[store.currentID])
	}

	file, mimeType, err := assetService.OpenImageForUser(context.Background(), "user-1", store.currentID)
	if err != nil {
		t.Fatalf("OpenImageForUser returned error: %v", err)
	}
	defer file.Close()
	if mimeType != "image/png" {
		t.Fatalf("MIME type = %q, want image/png", mimeType)
	}
	stored, err := os.ReadFile(filepath.Join(directory, store.currentID))
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if bytes.Equal(stored, imageBytes) {
		t.Fatal("cached image was not transformed")
	}
	if cached := store.assets[store.currentID]; cached.ByteSize != int64(len(stored)) {
		t.Fatalf("cached byte size = %d, want %d", cached.ByteSize, len(stored))
	}
}

func TestProcessPrefersFeedImageURL(t *testing.T) {
	imageBytes := testPNG(t, 1, 1)
	var pageRequests, imageRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/article":
			pageRequests++
			_, _ = w.Write([]byte(`<meta property="og:image" content="/page-image.png">`))
		case "/feed-image.png":
			imageRequests++
			_, _ = w.Write(imageBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	pageURL := "https://example.com/article"
	imageURL := "https://example.com/feed-image.png"
	store := newThumbnailStore(core.ItemImageInputs{PageURL: pageURL, ImageURL: imageURL})
	service := NewService(store, asset.NewService(store, t.TempDir()), testHTTPClient(t, server))
	if err := service.Process(context.Background(), JobArgs{ItemID: "item-1", PageURL: pageURL, ImageURL: imageURL}); err != nil {
		t.Fatalf("Process returned error: %v", err)
	}
	if pageRequests != 0 || imageRequests != 1 || store.currentID == "" {
		t.Fatalf("requests = (page %d, image %d), asset ID = %q", pageRequests, imageRequests, store.currentID)
	}
}

func TestProcessSkipsStaleImageInputs(t *testing.T) {
	pageURL := "https://example.com/article"
	store := newThumbnailStore(core.ItemImageInputs{PageURL: pageURL, ImageURL: "https://example.com/new.png"})
	service := NewService(store, asset.NewService(store, t.TempDir()), nil)
	if err := service.Process(context.Background(), JobArgs{ItemID: "item-1", PageURL: pageURL, ImageURL: "https://example.com/old.png"}); err != nil {
		t.Fatalf("Process returned error: %v", err)
	}
	if store.replaceCalls != 0 {
		t.Fatalf("stale job changed attachment %d times", store.replaceCalls)
	}
}

func TestProcessRemovesAttachmentWhenImageIsMissing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<title>No image</title>`))
	}))
	defer server.Close()
	pageURL := "https://example.com/article"
	store := newThumbnailStore(core.ItemImageInputs{PageURL: pageURL})
	store.currentID = "12345678-1234-1234-1234-123456789abc"
	store.assets[store.currentID] = core.Asset{ID: store.currentID, MediaType: "image/png"}
	service := NewService(store, asset.NewService(store, t.TempDir()), testHTTPClient(t, server))
	if err := service.Process(context.Background(), JobArgs{ItemID: "item-1", PageURL: pageURL}); err != nil {
		t.Fatalf("Process returned error: %v", err)
	}
	if store.currentID != "" {
		t.Fatalf("missing image left attachment %q", store.currentID)
	}
}

func TestValidateImageRejectsUnsupportedAndOversizedImages(t *testing.T) {
	if _, err := validateImage([]byte(`<svg></svg>`)); err == nil {
		t.Fatal("validateImage accepted SVG")
	}
	oversizedPNG := testPNG(t, maxImageDimension+1, 1)
	if _, err := validateImage(oversizedPNG); err == nil {
		t.Fatal("validateImage accepted oversized dimensions")
	}
}

func testHTTPClient(t *testing.T, server *httptest.Server) *http.Client {
	t.Helper()
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	return &http.Client{Transport: testRoundTripper{base: base, transport: server.Client().Transport}}
}

type testRoundTripper struct {
	base      *url.URL
	transport http.RoundTripper
}

func (r testRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	mapped := request.Clone(request.Context())
	mapped.URL.Scheme = r.base.Scheme
	mapped.URL.Host = r.base.Host
	response, err := r.transport.RoundTrip(mapped)
	if response != nil {
		response.Request = request
	}
	return response, err
}

func testPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatalf("png.Encode returned error: %v", err)
	}
	return buffer.Bytes()
}

type thumbnailStore struct {
	inputs       core.ItemImageInputs
	assets       map[string]core.Asset
	currentID    string
	replaceCalls int
}

func newThumbnailStore(inputs core.ItemImageInputs) *thumbnailStore {
	return &thumbnailStore{inputs: inputs, assets: make(map[string]core.Asset)}
}

func (s *thumbnailStore) FindItemImageInputs(_ context.Context, _ string) (core.ItemImageInputs, error) {
	return s.inputs, nil
}

func (s *thumbnailStore) ReplaceItemAssetIfInputsMatch(_ context.Context, _ string, inputs core.ItemImageInputs, _ string, asset *core.Asset) (string, bool, error) {
	if inputs != s.inputs {
		return "", false, nil
	}
	s.replaceCalls++
	previousID := s.currentID
	if asset == nil {
		s.currentID = ""
	} else {
		s.assets[asset.ID] = *asset
		s.currentID = asset.ID
	}
	return previousID, true, nil
}

func (s *thumbnailStore) FindAssetForUser(_ context.Context, _, id string) (core.Asset, error) {
	asset, ok := s.assets[id]
	if !ok || id != s.currentID {
		return core.Asset{}, core.ErrAssetNotFound
	}
	return asset, nil
}

func (s *thumbnailStore) ListAssets(_ context.Context) ([]core.Asset, error) {
	assets := make([]core.Asset, 0, len(s.assets))
	for _, asset := range s.assets {
		assets = append(assets, asset)
	}
	return assets, nil
}

func (s *thumbnailStore) DeleteOrphanAsset(_ context.Context, id string, before time.Time) (bool, error) {
	asset, ok := s.assets[id]
	if !ok || asset.CreatedAt.After(before) || id == s.currentID {
		return false, nil
	}
	delete(s.assets, id)
	return true, nil
}
