package chathistories

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/stretchr/testify/require"
)

// transparentSquareWebP is a 24x24 lossless WebP: a black 12x12 square on a
// transparent background, the shape of a monochrome custom emoji.
const transparentSquareWebP = "UklGRiwAAABXRUJQVlA4TB8AAAAvF8AFEA8QEfMfQlEDApCjfyfNfE2CiP5PAIapDzAEAA=="

func encodeTestPNG(t *testing.T, img image.Image) []byte {
	t.Helper()

	var buffer bytes.Buffer

	require.NoError(t, png.Encode(&buffer, img))

	return buffer.Bytes()
}

// glyphPNG draws a centered square of the given color on a transparent
// background.
func glyphPNG(t *testing.T, size int, glyph color.NRGBA) []byte {
	t.Helper()

	img := image.NewNRGBA(image.Rect(0, 0, size, size))

	for y := size / 4; y < size*3/4; y++ {
		for x := size / 4; x < size*3/4; x++ {
			img.SetNRGBA(x, y, glyph)
		}
	}

	return encodeTestPNG(t, img)
}

func solidPNG(t *testing.T, width, height int, fill func(x, y int) color.NRGBA) []byte {
	t.Helper()

	img := image.NewNRGBA(image.Rect(0, 0, width, height))

	for y := range height {
		for x := range width {
			img.SetNRGBA(x, y, fill(x, y))
		}
	}

	return encodeTestPNG(t, img)
}

func decodeTestImage(t *testing.T, data []byte) image.Image {
	t.Helper()

	img, err := png.Decode(bytes.NewReader(data))
	require.NoError(t, err)

	return img
}

func requireGray(t *testing.T, img image.Image, x, y int, expected uint8) {
	t.Helper()

	r, g, b, _ := img.At(x, y).RGBA()
	want := uint32(expected)

	require.Equal(t, []uint32{want, want, want}, []uint32{r >> 8, g >> 8, b >> 8}, "pixel %d,%d", x, y)
}

func TestPrepareImageFlattensDarkGlyphOntoWhite(t *testing.T) {
	webp, err := base64.StdEncoding.DecodeString(transparentSquareWebP)
	require.NoError(t, err)

	data, mimeType, err := prepareImage(webp, imageVariantContrast)
	require.NoError(t, err)
	require.Equal(t, "image/png", mimeType)

	img := decodeTestImage(t, data)
	bounds := img.Bounds()
	require.GreaterOrEqual(t, bounds.Dx(), 24*imageMaxScale, "small emoji are enlarged up to the scale cap")

	requireGray(t, img, 0, 0, 0xff)
	requireGray(t, img, bounds.Dx()/2, bounds.Dy()/2, 0x00)
}

func TestPrepareImageFlattensLightGlyphOntoDark(t *testing.T) {
	data, _, err := prepareImage(glyphPNG(t, 100, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}), imageVariantContrast)
	require.NoError(t, err)

	img := decodeTestImage(t, data)
	requireGray(t, img, 0, 0, 0x1e)
	requireGray(t, img, img.Bounds().Dx()/2, img.Bounds().Dy()/2, 0xff)

	inverse, _, err := prepareImage(glyphPNG(t, 100, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}), imageVariantInverse)
	require.NoError(t, err)
	requireGray(t, decodeTestImage(t, inverse), 0, 0, 0xff)
}

func TestPrepareImageDrawsSilhouette(t *testing.T) {
	data, _, err := prepareImage(glyphPNG(t, 100, color.NRGBA{R: 0xe0, G: 0x30, B: 0x30, A: 0xff}), imageVariantSilhouette)
	require.NoError(t, err)

	img := decodeTestImage(t, data)
	requireGray(t, img, 0, 0, 0xff)
	requireGray(t, img, img.Bounds().Dx()/2, img.Bounds().Dy()/2, 0x00)
}

func TestPrepareImageLeavesLargeOpaquePhotoAlone(t *testing.T) {
	photo := solidPNG(t, 400, 300, func(x, y int) color.NRGBA {
		return color.NRGBA{R: uint8(x % 256), G: uint8(y % 256), B: 0x80, A: 0xff} //nolint:gosec // reduced modulo 256 first
	})

	for _, variant := range []imageVariant{imageVariantContrast, imageVariantInverse, imageVariantSilhouette} {
		_, _, err := prepareImage(photo, variant)
		require.ErrorIs(t, err, errImageVariantNotApplicable)
	}

	_, mimeType, err := prepareImage(photo, imageVariantEnhance)
	require.NoError(t, err)
	require.Equal(t, "image/png", mimeType)
}

func TestPrepareImageEnlargesSmallOpaqueImage(t *testing.T) {
	data, _, err := prepareImage(solidPNG(t, 64, 64, func(int, int) color.NRGBA { return color.NRGBA{R: 0x80, A: 0xff} }), imageVariantContrast)
	require.NoError(t, err)
	require.GreaterOrEqual(t, decodeTestImage(t, data).Bounds().Dx(), imageMinSide)
}

func TestPrepareImageStretchesContrast(t *testing.T) {
	dim := solidPNG(t, 300, 300, func(x, _ int) color.NRGBA {
		level := uint8(100 + x*20/300) //nolint:gosec // x < 300 keeps the level within 100..119
		return color.NRGBA{R: level, G: level, B: level, A: 0xff}
	})

	data, _, err := prepareImage(dim, imageVariantEnhance)
	require.NoError(t, err)

	img := decodeTestImage(t, data)
	requireGray(t, img, 0, 150, 0x00)
	requireGray(t, img, 299, 150, 0xff)
}

// pngHeaderOnly returns a PNG whose header claims the given size but that
// holds no pixel data, the shape of a decompression bomb.
func pngHeaderOnly(width, height uint32) []byte {
	chunk := func(kind string, data []byte) []byte {
		var buffer bytes.Buffer

		_ = binary.Write(&buffer, binary.BigEndian, uint32(len(data))) //nolint:gosec // chunk data is a few bytes

		buffer.WriteString(kind)
		buffer.Write(data)
		_ = binary.Write(&buffer, binary.BigEndian, crc32.ChecksumIEEE(append([]byte(kind), data...)))

		return buffer.Bytes()
	}

	header := make([]byte, 13)
	binary.BigEndian.PutUint32(header[0:4], width)
	binary.BigEndian.PutUint32(header[4:8], height)
	header[8], header[9] = 8, 6 // 8-bit RGBA

	data := []byte("\x89PNG\r\n\x1a\n")
	data = append(data, chunk("IHDR", header)...)

	return append(data, chunk("IEND", nil)...)
}

func TestPrepareImageRefusesHugeDimensions(t *testing.T) {
	_, _, err := prepareImage(pngHeaderOnly(20000, 20000), imageVariantContrast)
	require.ErrorIs(t, err, errImageTooLarge)
}

func TestPrepareImageRejectsUndecodableData(t *testing.T) {
	_, _, err := prepareImage([]byte("not an image"), imageVariantContrast)
	require.Error(t, err)
	require.NotErrorIs(t, err, errImageVariantNotApplicable)
}
