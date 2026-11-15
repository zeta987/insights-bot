package chathistories

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	_ "image/gif"  // registers GIF decoding for image.Decode
	_ "image/jpeg" // registers JPEG decoding for image.Decode
	"image/png"

	"github.com/samber/lo"
	_ "golang.org/x/image/webp" // registers WebP decoding, the format of most stickers
)

// imageVariant is one way of rendering an image for the media model. Models
// read transparent pixels as black, so dark glyphs on a transparent sticker
// vanish unless the image is flattened onto a contrasting background.
type imageVariant int

const (
	// imageVariantContrast flattens transparency onto a background that
	// contrasts with the visible pixels and enlarges small images.
	imageVariantContrast imageVariant = iota
	// imageVariantInverse flattens onto the other background.
	imageVariantInverse
	// imageVariantSilhouette draws every visible pixel black on white, which
	// suits monochrome emoji that Telegram recolors.
	imageVariantSilhouette
	// imageVariantEnhance stretches the contrast of the flattened image, for
	// dim or washed-out pictures.
	imageVariantEnhance
	// imageVariantLight and imageVariantDark flatten onto a fixed background,
	// for when the check model asks for one by name.
	imageVariantLight
	imageVariantDark
)

const (
	imageMinSide        = 256
	imageMaxScale       = 8
	imageVisibleAlpha   = 0x1000 // about 6% opacity
	imageOpaqueAlpha    = 0xf000
	imageDarkContentLum = 0.55
)

// imageMaxPixels refuses images whose header promises more pixels than a
// sticker or photo needs, so a tiny file cannot make decoding allocate
// gigabytes.
const imageMaxPixels = 16 << 20

var errImageVariantNotApplicable = errors.New("image variant does not apply to this image")

var errImageTooLarge = errors.New("image is too large to render")

type imageStats struct {
	width, height  int
	hasTransparent bool
	darkContent    bool
}

func analyzeImage(img image.Image) imageStats {
	bounds := img.Bounds()
	stats := imageStats{width: bounds.Dx(), height: bounds.Dy()}

	var visible, transparent int

	var lumSum float64

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			if a < imageOpaqueAlpha {
				transparent++
			}
			if a < imageVisibleAlpha {
				continue
			}

			visible++
			// RGBA is premultiplied; divide by alpha for the real color.
			lumSum += (0.2126*float64(r) + 0.7152*float64(g) + 0.0722*float64(b)) / float64(a)
		}
	}

	total := stats.width * stats.height
	stats.hasTransparent = total > 0 && transparent*100 >= total
	stats.darkContent = visible == 0 || lumSum/float64(visible) < imageDarkContentLum

	return stats
}

func imageScale(stats imageStats) int {
	side := max(stats.width, stats.height)
	if side == 0 || side >= imageMinSide {
		return 1
	}

	return min((imageMinSide+side-1)/side, imageMaxScale)
}

// renderImage composites img onto background, scaled up by an integer factor
// with nearest-neighbour sampling so pixel art and glyph edges stay sharp. A
// silhouette paints every visible pixel black instead of its own color.
// Transparent images get a margin so glyphs do not touch the edge.
func renderImage(img image.Image, background color.NRGBA, scale int, silhouette bool, margin bool) *image.NRGBA {
	bounds := img.Bounds()

	padding := 0
	if margin {
		padding = max(bounds.Dx(), bounds.Dy()) * scale / 12
	}

	out := image.NewNRGBA(image.Rect(0, 0, bounds.Dx()*scale+2*padding, bounds.Dy()*scale+2*padding))

	for i := 0; i < len(out.Pix); i += 4 {
		out.Pix[i], out.Pix[i+1], out.Pix[i+2], out.Pix[i+3] = background.R, background.G, background.B, 0xff
	}

	bgR, bgG, bgB := float64(background.R)*257, float64(background.G)*257, float64(background.B)*257

	for y := 0; y < bounds.Dy()*scale; y++ {
		for x := 0; x < bounds.Dx()*scale; x++ {
			r, g, b, a := img.At(bounds.Min.X+x/scale, bounds.Min.Y+y/scale).RGBA()
			if silhouette {
				if a < imageVisibleAlpha {
					continue
				}

				r, g, b, a = 0, 0, 0, 0xffff
			}

			// Premultiplied source over an opaque background.
			rest := 1 - float64(a)/0xffff
			offset := out.PixOffset(x+padding, y+padding)
			out.Pix[offset] = uint8((float64(r) + bgR*rest) / 257)
			out.Pix[offset+1] = uint8((float64(g) + bgG*rest) / 257)
			out.Pix[offset+2] = uint8((float64(b) + bgB*rest) / 257)
		}
	}

	return out
}

// stretchContrast maps the 1st to 99th luminance percentile onto the full
// range, which brings out dim or low-contrast content.
func stretchContrast(img *image.NRGBA) {
	var histogram [256]int

	pixels := len(img.Pix) / 4

	for i := 0; i < len(img.Pix); i += 4 {
		histogram[(int(img.Pix[i])*2126+int(img.Pix[i+1])*7152+int(img.Pix[i+2])*722)/10000]++
	}

	low, high, seen := 0, 255, 0
	for level, count := range histogram {
		seen += count
		if seen*100 >= pixels {
			low = level
			break
		}
	}

	seen = 0
	for level := 255; level >= 0; level-- {
		seen += histogram[level]
		if seen*100 >= pixels {
			high = level
			break
		}
	}

	if high-low < 16 {
		low, high = max(0, (low+high)/2-8), min(255, (low+high)/2+8)
	}

	for i := 0; i < len(img.Pix); i += 4 {
		for c := range 3 {
			value := (int(img.Pix[i+c]) - low) * 255 / (high - low)
			img.Pix[i+c] = uint8(min(255, max(0, value)))
		}
	}
}

// prepareImage renders an image for the media model. It returns
// errImageVariantNotApplicable when the variant would not change anything,
// such as inverting the background of an opaque photo.
func prepareImage(data []byte, variant imageVariant) ([]byte, string, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, "", err
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > imageMaxPixels {
		return nil, "", errImageTooLarge
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", err
	}

	stats := analyzeImage(img)
	scale := imageScale(stats)

	light := color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	dark := color.NRGBA{R: 0x1e, G: 0x1e, B: 0x1e, A: 0xff}
	background := light

	if !stats.darkContent {
		background = dark
	}

	var rendered *image.NRGBA

	switch variant {
	case imageVariantContrast:
		if !stats.hasTransparent && scale == 1 {
			return nil, "", errImageVariantNotApplicable
		}

		rendered = renderImage(img, background, scale, false, stats.hasTransparent)
	case imageVariantInverse:
		if !stats.hasTransparent {
			return nil, "", errImageVariantNotApplicable
		}

		if background == light {
			background = dark
		} else {
			background = light
		}

		rendered = renderImage(img, background, scale, false, true)
	case imageVariantSilhouette:
		if !stats.hasTransparent {
			return nil, "", errImageVariantNotApplicable
		}

		rendered = renderImage(img, light, scale, true, true)
	case imageVariantEnhance:
		rendered = renderImage(img, background, scale, false, stats.hasTransparent)
		stretchContrast(rendered)
	case imageVariantLight, imageVariantDark:
		if !stats.hasTransparent {
			return nil, "", errImageVariantNotApplicable
		}

		rendered = renderImage(img, lo.Ternary(variant == imageVariantLight, light, dark), scale, false, true)
	default:
		return nil, "", errImageVariantNotApplicable
	}

	var buffer bytes.Buffer
	if err := png.Encode(&buffer, rendered); err != nil {
		return nil, "", err
	}

	return buffer.Bytes(), "image/png", nil
}
