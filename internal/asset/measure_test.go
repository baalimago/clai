package asset_test

import (
	"bytes"
	"errors"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/baalimago/clai/internal/asset"
)

// Proves Measure.1–2.
func TestMeasure(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 30, 20))
	encode := map[string]func(*bytes.Buffer) error{
		"png":  func(b *bytes.Buffer) error { return png.Encode(b, img) },
		"jpeg": func(b *bytes.Buffer) error { return jpeg.Encode(b, img, nil) },
		"gif":  func(b *bytes.Buffer) error { return gif.Encode(b, img, nil) },
	}
	for name, enc := range encode {
		var buf bytes.Buffer
		if err := enc(&buf); err != nil {
			t.Fatalf("encode %s: %v", name, err)
		}
		got, err := asset.Measure(buf.Bytes())
		if err != nil || got != (asset.Size{Width: 30, Height: 20}) {
			t.Errorf("%s: Measure = %+v, %v; want 30×20", name, got, err)
		}
	}
	for _, data := range [][]byte{[]byte("RIFF\x00\x00\x00\x00WEBPVP8 "), []byte("not an image"), nil} {
		if got, err := asset.Measure(data); !errors.Is(err, asset.ErrUnmeasurable) || got != (asset.Size{}) {
			t.Errorf("Measure(%q) = %+v, %v; want ErrUnmeasurable", data, got, err)
		}
	}
}
