package asset

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
)

// Size is an image's dimensions in pixels.
type Size struct {
	Width, Height int
}

var ErrUnmeasurable = errors.New("image dimensions unknown")

// Measure reads the dimensions from an image's header.
//  1. PNG, JPEG and GIF return their width and height.
//  2. Any other bytes, WebP included, fail with ErrUnmeasurable.
func Measure(data []byte) (Size, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return Size{}, fmt.Errorf("%w: %v", ErrUnmeasurable, err)
	}
	return Size{Width: cfg.Width, Height: cfg.Height}, nil
}
