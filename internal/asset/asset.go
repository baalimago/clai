// Package asset stores image bytes as content-addressed files.
package asset

import (
	"errors"
	"fmt"
	"strings"
)

// Image is image bytes with their MIME type.
type Image struct {
	Data []byte
	MIME string
}

type ImageLoader interface {
	// Load returns the image stored under ref.
	//  1. It returns bytes whose sha256 hex equals ref's stem, with the MIME type of
	//     ref's extension (README MIME-to-extension parameter).
	//  2. A ref with no stored file fails with ErrAssetNotFound.
	//  3. A stored file whose sha256 differs from ref's stem fails with ErrAssetCorrupt.
	//  4. A ref that is not 64 lowercase hex plus a known extension fails with ErrAssetRef.
	Load(ref string) (Image, error)
}

// SweepCount is the number of asset files a sweep removed.
type SweepCount int

type AssetStorer interface {
	ImageLoader
	// Put stores data and returns its ref.
	//  1. The ref is sha256hex(data) + "." + the extension of mime (README parameter).
	//  2. Put is idempotent. The same bytes give the same ref, and an existing file
	//     is not rewritten.
	//  3. A Put that fails leaves no file under the ref.
	//  4. A mime with no extension in the README parameter fails with ErrUnsupportedMIME.
	//  5. When Sniff(data) disagrees with mime, Put fails with
	//     *MIMEMismatchError{Declared, Sniffed}.
	//  6. The store's directory is created on the first Put. Construction never
	//     touches the disk.
	Put(data []byte, mime string) (string, error)
	// Resolve maps a handle to the one stored ref that starts with it (I1).
	//  1. Exactly one match returns its ref.
	//  2. No match fails with ErrUnknownHandle.
	//  3. More than one match fails with *AmbiguousHandleError{Handle, Refs}.
	//  4. A handle that is not handle-length lowercase hex fails with ErrHandle.
	Resolve(handle string) (string, error)
	// Path returns the absolute path of ref's file.
	//  1. A stored ref returns <dir>/<ref>.
	//  2. A missing file fails with ErrAssetNotFound. A malformed ref fails with ErrAssetRef.
	Path(ref string) (string, error)
	// Sweep removes every asset whose ref is not in live (C3).
	//  1. It returns the number of files removed.
	//  2. It leaves files whose names are not asset refs untouched.
	//  3. When live holds every stored ref, it removes nothing.
	//  4. A missing store directory sweeps 0 without error.
	Sweep(live map[string]struct{}) (SweepCount, error)
}

// Sniff returns the MIME type of image bytes from their signature.
//  1. PNG, JPEG, GIF87a/GIF89a and RIFF-WEBP map to their image/* type.
//  2. Anything else fails with ErrNotImage.
func Sniff(data []byte) (string, error) {
	for _, f := range formats() {
		if f.matches(data) {
			return f.mime, nil
		}
	}
	return "", ErrNotImage
}

// NewStore returns the on-disk store rooted at dir. It does not touch the disk (Put.6).
func NewStore(dir string) AssetStorer {
	return diskStore{dir: dir}
}

var (
	ErrAssetNotFound   = errors.New("asset not found")
	ErrAssetCorrupt    = errors.New("asset content does not match its ref")
	ErrAssetRef        = errors.New("malformed asset ref")
	ErrUnsupportedMIME = errors.New("unsupported image mime type")
	ErrUnknownHandle   = errors.New("no image with that handle")
	ErrHandle          = errors.New("malformed image handle")
	ErrNotImage        = errors.New("data is not a supported image")
)

// MIMEMismatchError.1: Error names Declared and Sniffed; Unwrap returns ErrUnsupportedMIME.
type MIMEMismatchError struct{ Declared, Sniffed string }

func (e *MIMEMismatchError) Error() string {
	return fmt.Sprintf("image declared as %s is %s", e.Declared, e.Sniffed)
}

func (e *MIMEMismatchError) Unwrap() error { return ErrUnsupportedMIME }

// AmbiguousHandleError.1: Error names Handle and every ref; Unwrap returns ErrUnknownHandle.
type AmbiguousHandleError struct {
	Handle string
	Refs   []string
}

func (e *AmbiguousHandleError) Error() string {
	return fmt.Sprintf("image handle %s is ambiguous: it matches %s", e.Handle, strings.Join(e.Refs, ", "))
}

func (e *AmbiguousHandleError) Unwrap() error { return ErrUnknownHandle }

// ImageTooLargeError is shared by every reader of local image files (phases 7, 9, 13).
// ImageTooLargeError.1: Error names Path, Size and the limit.
type ImageTooLargeError struct {
	Path        string
	Size, Limit int64
}

func (e *ImageTooLargeError) Error() string {
	return fmt.Sprintf("image %s is %d bytes, over the %d-byte limit", e.Path, e.Size, e.Limit)
}
