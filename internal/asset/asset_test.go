package asset_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/asset"
)

func TestSniff(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
		want string
	}{
		{"png", pngBytes(t, 30), "image/png"},
		{"jpeg", jpegBytes(t), "image/jpeg"},
		{"gif89a", gifBytes(t), "image/gif"},
		{"gif87a", gif87aBytes(), "image/gif"},
		{"webp", webpBytes(), "image/webp"},
	} {
		got, err := asset.Sniff(tc.data)
		if err != nil || got != tc.want {
			t.Fatalf("Sniff(%s) = %q, %v; want %q", tc.name, got, err, tc.want)
		}
	}
	for name, data := range map[string][]byte{
		"text":          []byte("just some text"),
		"empty":         nil,
		"riff not webp": []byte("RIFF\x1a\x00\x00\x00WAVEfmt "),
		"short riff":    []byte("RIFF"),
		"bmp":           []byte("BM\x3a\x00\x00\x00"),
	} {
		if got, err := asset.Sniff(data); !errors.Is(err, asset.ErrNotImage) {
			t.Fatalf("Sniff(%s) = %q, %v; want ErrNotImage", name, got, err)
		}
	}
}

func TestErrorTypes(t *testing.T) {
	mismatch := &asset.MIMEMismatchError{Declared: "image/jpeg", Sniffed: "image/png"}
	ambiguous := &asset.AmbiguousHandleError{Handle: "abcdef", Refs: []string{"abcdef01.png", "abcdef02.jpg"}}
	tooLarge := &asset.ImageTooLargeError{Path: "/tmp/cat.png", Size: 30 << 20, Limit: 20 << 20}
	for _, tc := range []struct {
		name     string
		err      error
		sentinel error
		names    []string
	}{
		{"MIMEMismatchError.1", mismatch, asset.ErrUnsupportedMIME, []string{"image/jpeg", "image/png"}},
		{"AmbiguousHandleError.1", ambiguous, asset.ErrUnknownHandle, []string{"abcdef", "abcdef01.png", "abcdef02.jpg"}},
		{"ImageTooLargeError.1", tooLarge, nil, []string{"/tmp/cat.png", "31457280", "20971520"}},
	} {
		if tc.sentinel != nil && !errors.Is(tc.err, tc.sentinel) {
			t.Fatalf("%s: errors.Is(%v, %v) = false", tc.name, tc.err, tc.sentinel)
		}
		for _, want := range tc.names {
			if !strings.Contains(tc.err.Error(), want) {
				t.Fatalf("%s: %q does not name %q", tc.name, tc.err.Error(), want)
			}
		}
	}
}
