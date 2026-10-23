package asset

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type format struct {
	mime, ext string
	matches   func(data []byte) bool
}

func formats() [4]format {
	return [4]format{
		{"image/png", "png", func(d []byte) bool { return bytes.HasPrefix(d, []byte("\x89PNG\r\n\x1a\n")) }},
		{"image/jpeg", "jpg", func(d []byte) bool { return bytes.HasPrefix(d, []byte("\xff\xd8\xff")) }},
		{"image/gif", "gif", func(d []byte) bool {
			return bytes.HasPrefix(d, []byte("GIF87a")) || bytes.HasPrefix(d, []byte("GIF89a"))
		}},
		{"image/webp", "webp", func(d []byte) bool {
			return len(d) >= 12 && bytes.HasPrefix(d, []byte("RIFF")) && string(d[8:12]) == "WEBP"
		}},
	}
}

// MIMEExtensions returns the README parameter "MIME type to file extension" as a
// fresh map, so a caller's edit never reaches the store.
func MIMEExtensions() map[string]string {
	exts := map[string]string{}
	for _, f := range formats() {
		exts[f.mime] = f.ext
	}
	return exts
}

func formatOfMIME(mime string) (format, error) {
	for _, f := range formats() {
		if f.mime == mime {
			return f, nil
		}
	}
	return format{}, fmt.Errorf("%w: %q", ErrUnsupportedMIME, mime)
}

func refFor(data []byte, mime string) (string, error) {
	f, err := formatOfMIME(mime)
	if err != nil {
		return "", err
	}
	sniffed, err := Sniff(data)
	if err != nil {
		return "", fmt.Errorf("declared %s: %w", mime, err)
	}
	if sniffed != mime {
		return "", &MIMEMismatchError{Declared: mime, Sniffed: sniffed}
	}
	return sha256Hex(data) + "." + f.ext, nil
}

func parseRef(ref string) (format, error) {
	stem, ext, _ := strings.Cut(ref, ".")
	if len(stem) == sha256.Size*2 && isLowerHex(stem) {
		for _, f := range formats() {
			if f.ext == ext {
				return f, nil
			}
		}
	}
	return format{}, fmt.Errorf("%w: %q", ErrAssetRef, ref)
}

func checkHandle(handle string) error {
	if len(handle) != pub_models.HandleLength || !isLowerHex(handle) {
		return fmt.Errorf("%w: %q", ErrHandle, handle)
	}
	return nil
}

func checkContent(ref string, data []byte) error {
	if stem, _, _ := strings.Cut(ref, "."); stem != sha256Hex(data) {
		return fmt.Errorf("%w: %s", ErrAssetCorrupt, ref)
	}
	return nil
}

func matchHandle(handle string, refs []string) (string, error) {
	var matches []string
	for _, ref := range refs {
		if strings.HasPrefix(ref, handle) {
			matches = append(matches, ref)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("%w: %s", ErrUnknownHandle, handle)
	case 1:
		return matches[0], nil
	}
	return "", &AmbiguousHandleError{Handle: handle, Refs: matches}
}

func isLowerHex(s string) bool {
	for _, c := range []byte(s) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
