package thumbnail

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math/rand"
	"testing"

	"github.com/spool-reader/spool/internal/asset"
)

func TestTransformImageCenterCropsAndCompressesOpaquePNG(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 800, 800))
	colors := []color.RGBA{
		{R: 240, A: 255},
		{G: 240, A: 255},
		{B: 240, A: 255},
	}
	for y := 0; y < source.Bounds().Dy(); y++ {
		for x := 0; x < source.Bounds().Dx(); x++ {
			colorIndex := 1
			if y < 150 {
				colorIndex = 0
			} else if y >= 650 {
				colorIndex = 2
			}
			source.SetRGBA(x, y, colors[colorIndex])
		}
	}
	var sourceBuffer bytes.Buffer
	if err := png.Encode(&sourceBuffer, source); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}

	transformed, mediaType, err := transformImage(sourceBuffer.Bytes(), asset.MediaTypePNG)
	if err != nil {
		t.Fatalf("transformImage returned error: %v", err)
	}
	if mediaType != asset.MediaTypeJPEG {
		t.Fatalf("media type = %q, want %q", mediaType, asset.MediaTypeJPEG)
	}
	if len(transformed) >= sourceBuffer.Len() {
		t.Fatalf("transformed size = %d, source size = %d; want smaller", len(transformed), sourceBuffer.Len())
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(transformed))
	if err != nil {
		t.Fatalf("DecodeConfig: %v", err)
	}
	if format != "jpeg" || config.Width != thumbnailMaxWidth || config.Height != thumbnailMaxHeight {
		t.Fatalf("output = %s %dx%d, want jpeg %dx%d", format, config.Width, config.Height, thumbnailMaxWidth, thumbnailMaxHeight)
	}
	result, _, err := image.Decode(bytes.NewReader(transformed))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	for _, y := range []int{20, result.Bounds().Dy() / 2, result.Bounds().Dy() - 20} {
		r, _, b, _ := result.At(result.Bounds().Dx()/2, y).RGBA()
		if r > b+5000 {
			t.Fatalf("pixel at y=%d is outside the center crop: red=%d blue=%d", y, r, b)
		}
	}
}

func TestTransformImageAppliesJPEGOrientationBeforeCropping(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 800, 400))
	for y := 0; y < source.Bounds().Dy(); y++ {
		pixelColor := color.RGBA{G: 240, A: 255}
		if y < 100 {
			pixelColor = color.RGBA{R: 240, A: 255}
		} else if y >= 300 {
			pixelColor = color.RGBA{B: 240, A: 255}
		}
		for x := 0; x < source.Bounds().Dx(); x++ {
			source.SetRGBA(x, y, pixelColor)
		}
	}
	var sourceBuffer bytes.Buffer
	if err := jpeg.Encode(&sourceBuffer, source, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatalf("jpeg.Encode: %v", err)
	}
	oriented := addJPEGOrientation(sourceBuffer.Bytes(), 6)

	transformed, mediaType, err := transformImage(oriented, asset.MediaTypeJPEG)
	if err != nil {
		t.Fatalf("transformImage returned error: %v", err)
	}
	if mediaType != asset.MediaTypeJPEG {
		t.Fatalf("media type = %q, want %q", mediaType, asset.MediaTypeJPEG)
	}
	result, _, err := image.Decode(bytes.NewReader(transformed))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	leftRed, _, leftBlue, _ := result.At(20, result.Bounds().Dy()/2).RGBA()
	rightRed, _, rightBlue, _ := result.At(result.Bounds().Dx()-20, result.Bounds().Dy()/2).RGBA()
	if leftBlue <= leftRed || rightRed <= rightBlue {
		t.Fatalf("oriented crop colors = (left r/b %d/%d, right r/b %d/%d), want blue left and red right", leftRed, leftBlue, rightRed, rightBlue)
	}
}

func TestTransformImagePreservesTransparentPNGWithoutUpscaling(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 160, 100))
	source.SetNRGBA(80, 50, color.NRGBA{R: 255, A: 255})
	var sourceBuffer bytes.Buffer
	if err := png.Encode(&sourceBuffer, source); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}

	transformed, mediaType, err := transformImage(sourceBuffer.Bytes(), asset.MediaTypePNG)
	if err != nil {
		t.Fatalf("transformImage returned error: %v", err)
	}
	if mediaType != asset.MediaTypePNG {
		t.Fatalf("media type = %q, want %q", mediaType, asset.MediaTypePNG)
	}
	result, format, err := image.Decode(bytes.NewReader(transformed))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if format != "png" || result.Bounds().Dx() != 160 || result.Bounds().Dy() != 100 {
		t.Fatalf("output = %s %dx%d, want png 160x100", format, result.Bounds().Dx(), result.Bounds().Dy())
	}
	if _, _, _, alpha := result.At(0, 0).RGBA(); alpha != 0 {
		t.Fatalf("transparent pixel alpha = %d, want 0", alpha)
	}
}

func TestTransformImagePreservesGIFAnimationBytes(t *testing.T) {
	frame := image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{color.Black, color.White})
	frame.SetColorIndex(0, 0, 1)
	var sourceBuffer bytes.Buffer
	if err := gif.EncodeAll(&sourceBuffer, &gif.GIF{
		Image:     []*image.Paletted{frame, frame},
		Delay:     []int{5, 5},
		LoopCount: -1,
	}); err != nil {
		t.Fatalf("gif.EncodeAll: %v", err)
	}

	transformed, mediaType, err := transformImage(sourceBuffer.Bytes(), asset.MediaTypeGIF)
	if err != nil {
		t.Fatalf("transformImage returned error: %v", err)
	}
	if mediaType != asset.MediaTypeGIF || !bytes.Equal(transformed, sourceBuffer.Bytes()) {
		t.Fatal("animated GIF was modified")
	}
}

func TestTransformImageOpaqueNoisyPNGCompresses(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, thumbnailMaxWidth, thumbnailMaxHeight))
	random := rand.New(rand.NewSource(1))
	for y := 0; y < source.Bounds().Dy(); y++ {
		for x := 0; x < source.Bounds().Dx(); x++ {
			source.SetRGBA(x, y, color.RGBA{R: uint8(random.Intn(256)), G: uint8(random.Intn(256)), B: uint8(random.Intn(256)), A: 255})
		}
	}
	var sourceBuffer bytes.Buffer
	if err := png.Encode(&sourceBuffer, source); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	transformed, mediaType, err := transformImage(sourceBuffer.Bytes(), asset.MediaTypePNG)
	if err != nil {
		t.Fatalf("transformImage returned error: %v", err)
	}
	if mediaType != asset.MediaTypeJPEG || len(transformed) >= sourceBuffer.Len() {
		t.Fatalf("output = %s %d bytes, source = %d bytes", mediaType, len(transformed), sourceBuffer.Len())
	}
}

func addJPEGOrientation(data []byte, orientation byte) []byte {
	payload := []byte("Exif\x00\x00")
	payload = append(payload, []byte{
		'I', 'I', 42, 0, 8, 0, 0, 0,
		1, 0,
		0x12, 0x01, 3, 0, 1, 0, 0, 0,
		orientation, 0, 0, 0,
		0, 0, 0, 0,
	}...)
	length := len(payload) + 2
	segment := []byte{0xff, 0xe1, byte(length >> 8), byte(length)}
	result := append([]byte(nil), data[:2]...)
	result = append(result, segment...)
	result = append(result, payload...)
	return append(result, data[2:]...)
}
