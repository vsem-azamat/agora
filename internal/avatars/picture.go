// Package avatars keeps the pictures that stand for agents and people outside Agora: it accepts
// an image only after drawing it again (a raster) or writing it anew from an allowlist (an SVG),
// and stores each picture once under the SHA-256 of what it stored.
package avatars

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"mime"

	"golang.org/x/image/draw"
	"golang.org/x/image/webp"

	"github.com/vsem-azamat/agora/internal/store"
)

// Media types of avatars: the ones accepted, and the ones stored.
const (
	PNG  = "image/png"
	JPEG = "image/jpeg"
	WebP = "image/webp" // accepted, stored as PNG or JPEG
	SVG  = "image/svg+xml"
)

const (
	// MaxRaster bounds a PNG, JPEG or WebP image, in bytes.
	MaxRaster = 256 << 10
	// MaxSVG bounds an SVG image, in bytes.
	MaxSVG = 32 << 10
	// MaxSide bounds a side of a PNG, JPEG or WebP image, in pixels.
	MaxSide = 1024
	// LargeSide is the side of the stored picture, in pixels, when the image is at least that large.
	LargeSide = 256
	// SmallSide is the side of the smaller picture stored with a larger one, in pixels.
	SmallSide = 128
	// jpegQuality is the quality an opaque picture is stored with.
	jpegQuality = 85
)

// Accepted names the accepted kinds for errors.
const Accepted = "PNG, JPEG or WebP up to 256 KiB and 1024×1024, or SVG up to 32 KiB"

// Picture is an avatar ready to store.
type Picture struct {
	Hash  string // SHA-256 of Data, lowercase hex
	MIME  string // PNG, JPEG or SVG
	Data  []byte // at most LargeSide pixels a side
	Small []byte // the same at SmallSide pixels a side; nil when Data is not larger
	W, H  int    // pixels of Data; 0 for an SVG
}

// Prepare checks an image and makes the picture to store from it: a PNG, JPEG or WebP image is
// decoded, cut to its largest centered square and encoded again; an SVG image is checked against
// the allowlist and written anew. declared, the media type the sender gave, may be empty; when it
// is not, it must agree with the content.
func Prepare(data []byte, declared string) (Picture, error) {
	kind := sniff(data)
	if declared != "" {
		d, _, err := mime.ParseMediaType(declared)
		if err != nil || d != kind {
			if kind == "" {
				return Picture{}, store.Refuse(store.ErrInvalid, "not an image the board accepts: %s", Accepted)
			}
			return Picture{}, store.Refuse(store.ErrInvalid, "the image is %s, not %s", kind, declared)
		}
	}
	switch kind {
	case PNG, JPEG, WebP:
		return raster(data, kind)
	case SVG:
		if len(data) > MaxSVG {
			return Picture{}, store.Refuse(store.ErrInvalid, "an SVG avatar is at most %d KiB; this one is %d KiB", MaxSVG>>10, kib(len(data)))
		}
		clean, err := sanitize(data)
		if err != nil {
			return Picture{}, err
		}
		return Picture{Hash: hash(clean), MIME: SVG, Data: clean}, nil
	}
	return Picture{}, store.Refuse(store.ErrInvalid, "not an image the board accepts: %s", Accepted)
}

func kib(n int) int { return (n + 1023) >> 10 }

func hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// sniff names the kind of image data holds by its first bytes, or "".
func sniff(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return PNG
	case bytes.HasPrefix(data, []byte{0xff, 0xd8, 0xff}):
		return JPEG
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return WebP
	}
	t := bytes.TrimLeft(bytes.TrimPrefix(data, utf8BOM), " \t\r\n")
	if bytes.HasPrefix(t, []byte("<")) {
		return SVG // the allowlist decides whether it is one
	}
	return ""
}

type codec struct {
	config func(io.Reader) (image.Config, error)
	decode func(io.Reader) (image.Image, error)
}

var codecs = map[string]codec{
	PNG:  {png.DecodeConfig, png.Decode},
	JPEG: {jpeg.DecodeConfig, jpeg.Decode},
	WebP: {webp.DecodeConfig, webp.Decode},
}

// raster draws a PNG, JPEG or WebP image again: the largest centered square, at most LargeSide
// pixels a side, and SmallSide too when it is larger.
func raster(data []byte, kind string) (p Picture, err error) {
	if len(data) > MaxRaster {
		return Picture{}, store.Refuse(store.ErrInvalid, "an avatar is at most %d KiB; this one is %d KiB", MaxRaster>>10, kib(len(data)))
	}
	defer func() { // a decoder that panics on a crafted file refuses it
		if recover() != nil {
			p, err = Picture{}, store.Refuse(store.ErrInvalid, "the image cannot be decoded")
		}
	}()
	c := codecs[kind]
	cfg, err := c.config(bytes.NewReader(data))
	if err != nil {
		return Picture{}, store.Refuse(store.ErrInvalid, "the image cannot be decoded: %v", err)
	}
	if cfg.Width > MaxSide || cfg.Height > MaxSide {
		return Picture{}, store.Refuse(store.ErrInvalid, "an avatar is at most %d×%d pixels; this one is %d×%d", MaxSide, MaxSide, cfg.Width, cfg.Height)
	}
	if cfg.Width < 1 || cfg.Height < 1 {
		return Picture{}, store.Refuse(store.ErrInvalid, "the image has no pixels")
	}
	img, err := c.decode(bytes.NewReader(data))
	if err != nil {
		return Picture{}, store.Refuse(store.ErrInvalid, "the image cannot be decoded: %v", err)
	}
	b := img.Bounds()
	side := min(b.Dx(), b.Dy())
	square := image.Rect(0, 0, side, side).Add(b.Min).Add(image.Pt((b.Dx()-side)/2, (b.Dy()-side)/2))
	large := scale(img, square, min(side, LargeSide))
	p.MIME = PNG
	if large.Opaque() {
		p.MIME = JPEG
	}
	if p.Data, err = encode(large, p.MIME); err != nil {
		return Picture{}, err
	}
	if side > SmallSide {
		if p.Small, err = encode(scale(img, square, SmallSide), p.MIME); err != nil {
			return Picture{}, err
		}
	}
	p.Hash, p.W, p.H = hash(p.Data), large.Bounds().Dx(), large.Bounds().Dy()
	return p, nil
}

// scale draws the part r of img as a square of side pixels.
func scale(img image.Image, r image.Rectangle, side int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, side, side))
	if r.Dx() == side {
		draw.Draw(dst, dst.Bounds(), img, r.Min, draw.Src)
	} else {
		draw.CatmullRom.Scale(dst, dst.Bounds(), img, r, draw.Src, nil)
	}
	return dst
}

func encode(img image.Image, kind string) ([]byte, error) {
	var buf bytes.Buffer
	var err error
	if kind == JPEG {
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality})
	} else {
		err = (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&buf, img)
	}
	if err != nil {
		return nil, fmt.Errorf("encode avatar: %w", err)
	}
	return buf.Bytes(), nil
}
