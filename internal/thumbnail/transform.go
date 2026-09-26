package thumbnail

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math"

	imagedraw "golang.org/x/image/draw"

	"github.com/spool-reader/spool/internal/asset"
)

const (
	thumbnailAspectWidth  = 16
	thumbnailAspectHeight = 10
	thumbnailMaxWidth     = 480
	thumbnailMaxHeight    = 300
	thumbnailJPEGQuality  = 82

	jpegMarkerPrefix                = 0xff
	jpegSOIMarker                   = 0xd8
	jpegSOSMarker                   = 0xda
	jpegEOIMarker                   = 0xd9
	jpegAPP1Marker                  = 0xe1
	jpegTEMMarker                   = 0x01
	jpegRestartMarkerFirst          = 0xd0
	jpegRestartMarkerLast           = 0xd7
	jpegSegmentLengthSize           = 2
	exifHeader                      = "Exif\x00\x00"
	exifOrientationTag              = 0x0112
	exifOrientationTypeShort        = 3
	exifOrientationCount            = 1
	exifTiffMagic                   = 42
	tiffEntryCountSize              = 2
	tiffEntrySize                   = 12
	jpegOrientationNormal           = 1
	jpegOrientationMirrorHorizontal = 2
	jpegOrientationRotate180        = 3
	jpegOrientationMirrorVertical   = 4
	jpegOrientationTranspose        = 5
	jpegOrientationRotate90         = 6
	jpegOrientationTransverse       = 7
	jpegOrientationRotate270        = 8
)

func transformImage(data []byte, mediaType string) ([]byte, string, error) {
	switch mediaType {
	case asset.MediaTypeGIF:
		return data, mediaType, nil
	case asset.MediaTypeJPEG, asset.MediaTypePNG:
	default:
		return nil, "", errors.New("unsupported thumbnail image media type")
	}

	decoded, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", err
	}
	if (mediaType == asset.MediaTypeJPEG && format != "jpeg") || (mediaType == asset.MediaTypePNG && format != "png") {
		return nil, "", errors.New("thumbnail image format does not match media type")
	}

	if format == "jpeg" {
		decoded = applyJPEGOrientation(decoded, jpegOrientation(data))
	}
	cropped := centerCrop(decoded.Bounds())
	width, height := thumbnailDimensions(cropped.Dx(), cropped.Dy())
	resized := image.NewNRGBA(image.Rect(0, 0, width, height))
	imagedraw.CatmullRom.Scale(resized, resized.Bounds(), decoded, cropped, imagedraw.Src, nil)

	var output bytes.Buffer
	if hasTransparency(resized) {
		encoder := png.Encoder{CompressionLevel: png.BestCompression}
		if err := encoder.Encode(&output, resized); err != nil {
			return nil, "", err
		}
		return output.Bytes(), asset.MediaTypePNG, nil
	}
	if err := jpeg.Encode(&output, resized, &jpeg.Options{Quality: thumbnailJPEGQuality}); err != nil {
		return nil, "", err
	}
	return output.Bytes(), asset.MediaTypeJPEG, nil
}

func centerCrop(bounds image.Rectangle) image.Rectangle {
	width, height := bounds.Dx(), bounds.Dy()
	cropWidth, cropHeight := width, height

	if int64(width)*thumbnailAspectHeight > int64(height)*thumbnailAspectWidth {
		cropWidth = height * thumbnailAspectWidth / thumbnailAspectHeight
		if cropWidth == 0 {
			cropWidth = 1
		}
	} else if int64(width)*thumbnailAspectHeight < int64(height)*thumbnailAspectWidth {
		cropHeight = width * thumbnailAspectHeight / thumbnailAspectWidth
		if cropHeight == 0 {
			cropHeight = 1
		}
	}

	minX := bounds.Min.X + (width-cropWidth)/2
	minY := bounds.Min.Y + (height-cropHeight)/2
	return image.Rect(minX, minY, minX+cropWidth, minY+cropHeight)
}

func thumbnailDimensions(width, height int) (int, int) {
	scale := math.Min(1, math.Min(float64(thumbnailMaxWidth)/float64(width), float64(thumbnailMaxHeight)/float64(height)))
	return max(1, int(math.Round(float64(width)*scale))), max(1, int(math.Round(float64(height)*scale)))
}

func jpegOrientation(data []byte) int {
	if len(data) < jpegSegmentLengthSize || data[0] != jpegMarkerPrefix || data[1] != jpegSOIMarker {
		return 1
	}
	for offset := 2; offset+1 < len(data); {
		if data[offset] != jpegMarkerPrefix {
			return 1
		}
		for offset < len(data) && data[offset] == 0xff {
			offset++
		}
		if offset >= len(data) {
			return 1
		}
		marker := data[offset]
		offset++
		if marker == jpegSOSMarker || marker == jpegEOIMarker {
			return 1
		}
		if marker == jpegTEMMarker || marker >= jpegRestartMarkerFirst && marker <= jpegRestartMarkerLast {
			continue
		}
		if offset+jpegSegmentLengthSize > len(data) {
			return 1
		}
		length := int(binary.BigEndian.Uint16(data[offset : offset+2]))
		if length < 2 || length > len(data)-offset {
			return 1
		}
		segment := data[offset+jpegSegmentLengthSize : offset+length]
		if marker == jpegAPP1Marker && bytes.HasPrefix(segment, []byte(exifHeader)) {
			return exifOrientation(segment[len(exifHeader):])
		}
		offset += length
	}
	return 1
}

func exifOrientation(data []byte) int {
	if len(data) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(data[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	if order.Uint16(data[2:4]) != exifTiffMagic {
		return 1
	}
	ifdOffsetValue := order.Uint32(data[4:8])
	if uint64(ifdOffsetValue) > uint64(len(data)-tiffEntryCountSize) {
		return 1
	}
	ifdOffset := int(ifdOffsetValue)
	entryCount := int(order.Uint16(data[ifdOffset : ifdOffset+tiffEntryCountSize]))
	entries := ifdOffset + tiffEntryCountSize
	for index := 0; index < entryCount; index++ {
		entry := entries + index*tiffEntrySize
		if entry+tiffEntrySize > len(data) {
			return 1
		}
		if order.Uint16(data[entry:entry+2]) != exifOrientationTag || order.Uint16(data[entry+2:entry+4]) != exifOrientationTypeShort || order.Uint32(data[entry+4:entry+8]) != exifOrientationCount {
			continue
		}
		orientation := int(order.Uint16(data[entry+8 : entry+10]))
		if orientation >= jpegOrientationNormal && orientation <= jpegOrientationRotate270 {
			return orientation
		}
		return 1
	}
	return 1
}

type orientedImage struct {
	source      image.Image
	orientation int
}

func applyJPEGOrientation(source image.Image, orientation int) image.Image {
	if orientation <= jpegOrientationNormal || orientation > jpegOrientationRotate270 {
		return source
	}
	return orientedImage{source: source, orientation: orientation}
}

func (img orientedImage) ColorModel() color.Model { return img.source.ColorModel() }

func (img orientedImage) Bounds() image.Rectangle {
	bounds := img.source.Bounds()
	if img.orientation >= jpegOrientationTranspose && img.orientation <= jpegOrientationRotate270 {
		return image.Rect(0, 0, bounds.Dy(), bounds.Dx())
	}
	return image.Rect(0, 0, bounds.Dx(), bounds.Dy())
}

func (img orientedImage) At(x, y int) color.Color {
	bounds := img.source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	var sourceX, sourceY int
	switch img.orientation {
	case jpegOrientationMirrorHorizontal:
		sourceX, sourceY = width-1-x, y
	case jpegOrientationRotate180:
		sourceX, sourceY = width-1-x, height-1-y
	case jpegOrientationMirrorVertical:
		sourceX, sourceY = x, height-1-y
	case jpegOrientationTranspose:
		sourceX, sourceY = y, x
	case jpegOrientationRotate90:
		sourceX, sourceY = y, height-1-x
	case jpegOrientationTransverse:
		sourceX, sourceY = width-1-y, height-1-x
	case jpegOrientationRotate270:
		sourceX, sourceY = width-1-y, x
	default:
		sourceX, sourceY = x, y
	}
	return img.source.At(bounds.Min.X+sourceX, bounds.Min.Y+sourceY)
}

func hasTransparency(img image.Image) bool {
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			_, _, _, alpha := img.At(x, y).RGBA()
			if alpha < 0xffff {
				return true
			}
		}
	}
	return false
}
