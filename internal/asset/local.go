package asset

import (
	"fmt"
	"io"
	"io/fs"
	"os"
)

// LargestLocalImage is the README parameter "Largest local image file read", in bytes.
const LargestLocalImage = 20 << 20

// ReadLocalImage reads the image file at path, used as given, with its sniffed MIME type.
// A file over LargestLocalImage fails with *ImageTooLargeError before it is opened.
// Every other failure is wrapped as "image <path>: <cause>": the os error, fs.ErrInvalid
// for a file that is not regular, or ErrNotImage.
func ReadLocalImage(path string) (Image, error) {
	fail := func(err error) (Image, error) {
		return Image{}, fmt.Errorf("image %s: %w", path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fail(err)
	}
	if !info.Mode().IsRegular() {
		return fail(fmt.Errorf("%w: not a regular file", fs.ErrInvalid))
	}
	if info.Size() > LargestLocalImage {
		return Image{}, &ImageTooLargeError{Path: path, Size: info.Size(), Limit: LargestLocalImage}
	}
	f, err := os.Open(path)
	if err != nil {
		return fail(err)
	}
	defer f.Close()
	// The file may grow after the stat, so the read stops one byte past the limit.
	data, err := io.ReadAll(io.LimitReader(f, LargestLocalImage+1))
	if err != nil {
		return fail(err)
	}
	if int64(len(data)) > LargestLocalImage {
		return Image{}, &ImageTooLargeError{Path: path, Size: int64(len(data)), Limit: LargestLocalImage}
	}
	mime, err := Sniff(data)
	if err != nil {
		return fail(err)
	}
	return Image{Data: data, MIME: mime}, nil
}
