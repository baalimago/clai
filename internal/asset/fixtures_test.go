package asset_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"
)

func onePixel(shade uint8) *image.Gray {
	img := image.NewGray(image.Rect(0, 0, 1, 1))
	img.Pix[0] = shade
	return img
}

func pngBytes(t *testing.T, shade uint8) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, onePixel(shade)); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func jpegBytes(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, onePixel(7), nil); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}

func gifBytes(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := gif.Encode(&buf, onePixel(9), nil); err != nil {
		t.Fatalf("encode gif: %v", err)
	}
	return buf.Bytes()
}

func gif87aBytes() []byte {
	return append([]byte("GIF87a"), 1, 0, 1, 0, 0, 0, 0, ';')
}

func webpBytes() []byte {
	return []byte("RIFF\x1a\x00\x00\x00WEBPVP8L\x0d\x00\x00\x00\x2f\x00\x00\x00\x10\x07\x10\x11\x11\x88\x88\xfe\x07\x00")
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
