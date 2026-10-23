package assettest

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
)

// PNG returns a valid one-pixel PNG whose green channel is shade, so each shade gives other bytes.
func PNG(shade uint8) ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{G: shade, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("encode png: %w", err)
	}
	return buf.Bytes(), nil
}
