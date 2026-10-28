// Package imageinput turns the user's -img, -g and stdin inputs into pending images and prompt text.
package imageinput

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"

	"github.com/baalimago/clai/internal/asset"
	"github.com/baalimago/clai/internal/glob"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type CollectRequest struct {
	Images       []string  // -img/-image values in flag order: paths or globs, "~" allowed
	Glob         string    // -g/-glob value; "" when unset
	Stdin        io.Reader // nil when stdin is a terminal
	Args         []string  // the command's positional args, command name first, as utils.Prompt takes them
	StdinReplace string    // the -I/-i token, as utils.Prompt takes it
}

type Collected struct {
	Images       []pub_models.ImagePart // pending, Origin user, in order: -img, then -g, then stdin
	Prompt       string                 // the user's prompt text
	GlobMessages []pub_models.Message   // text messages for -g matches that are not images
}

type ImageCollector interface {
	// Collect gathers the inputs.
	//  1. Each -img value is expanded ("~", then glob). Matches are taken in lexical order,
	//     and each file becomes a pending ImagePart with MIME asset.Sniff(bytes).
	//  2. An -img value that matches nothing fails with *NoMatchError{Pattern}.
	//  3. A file matched by -img that is not an image fails with asset.ErrNotImage,
	//     naming the path.
	//  4. A file larger than the collector's limit (New: the README's largest-local-image
	//     parameter) fails with *asset.ImageTooLargeError{Path, Size, Limit}, before the
	//     file is read.
	//  5. A file that cannot be read fails with the os error, wrapped with the path.
	//  6. A -g match that sniffs as an image becomes an ImagePart. Every other match
	//     becomes a GlobMessages entry, exactly as the existing glob path builds its file
	//     messages today, in its order. The system and "#####" framing messages are not
	//     included.
	//  7. Stdin whose bytes sniff as an image becomes one ImagePart. So does stdin that,
	//     once surrounding whitespace is trimmed, is canonical standard base64 decoding to
	//     bytes that sniff as an image. In both cases Prompt is built from Args alone, by
	//     the no-stdin rule of 8.
	//  8. For any other stdin, and for nil Stdin, Prompt equals what utils.Prompt returned
	//     before this worklog for the same Args, StdinReplace and stdin content, including
	//     its no-prompt error (I10).
	//  9. Collect never writes to the asset store (I6).
	Collect(req CollectRequest) (Collected, error)
}

func New() ImageCollector { return NewWithLimit(asset.LargestLocalImage) }

// NewWithLimit collects with its own largest image size, in bytes.
func NewWithLimit(limit int64) ImageCollector { return collector{limit: limit} }

// NoMatchError.1: Error names Pattern.
type NoMatchError struct{ Pattern string }

func (e *NoMatchError) Error() string {
	return fmt.Sprintf("image pattern %q matches no file", e.Pattern)
}

type collector struct{ limit int64 }

func (c collector) Collect(req CollectRequest) (Collected, error) {
	var out Collected
	for _, pattern := range req.Images {
		images, err := c.imagesMatching(pattern)
		if err != nil {
			return Collected{}, err
		}
		out.Images = append(out.Images, images...)
	}
	if req.Glob != "" {
		images, messages, err := c.splitGlob(req.Glob)
		if err != nil {
			return Collected{}, err
		}
		out.Images = append(out.Images, images...)
		out.GlobMessages = messages
	}
	stdin := req.Stdin
	if stdin != nil {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return Collected{}, fmt.Errorf("failed to read stdin: %w", err)
		}
		image, err := c.stdinImage(data)
		switch {
		case err == nil:
			out.Images = append(out.Images, image)
			stdin = nil
		case errors.Is(err, asset.ErrNotImage):
			stdin = bytes.NewReader(data)
		default:
			return Collected{}, err
		}
	}
	prompt, err := utils.PromptFrom(req.StdinReplace, slices.Clone(req.Args), stdin)
	if err != nil {
		return Collected{}, err
	}
	out.Prompt = prompt
	return out, nil
}

func (c collector) imagesMatching(pattern string) ([]pub_models.ImagePart, error) {
	expanded, err := utils.ReplaceTildeWithHome(pattern)
	if err != nil {
		return nil, fmt.Errorf("image pattern %q: %w", pattern, err)
	}
	paths, err := filepath.Glob(expanded)
	if err != nil {
		return nil, fmt.Errorf("image pattern %q: %w", pattern, err)
	}
	if len(paths) == 0 {
		return nil, &NoMatchError{Pattern: pattern}
	}
	slices.Sort(paths)
	images := make([]pub_models.ImagePart, 0, len(paths))
	for _, path := range paths {
		image, err := c.readImageFile(path)
		if err != nil {
			return nil, err
		}
		images = append(images, image)
	}
	return images, nil
}

func (c collector) readImageFile(path string) (pub_models.ImagePart, error) {
	img, err := asset.ReadLocalImageUpTo(path, c.limit)
	if err != nil {
		return pub_models.ImagePart{}, err
	}
	return pub_models.ImagePart{MIME: img.MIME, Origin: pub_models.OriginUser, Data: img.Data}, nil
}

// userImage caps a -g or stdin image once read; -img files are capped before the read.
func (c collector) userImage(data []byte, source string) (pub_models.ImagePart, error) {
	mime, err := asset.Sniff(data)
	if err != nil {
		return pub_models.ImagePart{}, fmt.Errorf("image %s: %w", source, err)
	}
	if size := int64(len(data)); size > c.limit {
		return pub_models.ImagePart{}, &asset.ImageTooLargeError{Path: source, Size: size, Limit: c.limit}
	}
	return pub_models.ImagePart{MIME: mime, Origin: pub_models.OriginUser, Data: data}, nil
}

func (c collector) splitGlob(pattern string) ([]pub_models.ImagePart, []pub_models.Message, error) {
	files, err := glob.ReadFiles(pattern)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse glob string: '%v', err: %w", pattern, err)
	}
	var images []pub_models.ImagePart
	var messages []pub_models.Message
	for _, f := range files {
		image, err := c.userImage(f.Data, f.Name)
		switch {
		case err == nil:
			images = append(images, image)
		case errors.Is(err, asset.ErrNotImage):
			messages = append(messages, glob.FileMessage(f))
		default:
			return nil, nil, err
		}
	}
	return images, messages, nil
}

// stdinImage fails with asset.ErrNotImage unless stdin is an image, raw or as canonical base64.
func (c collector) stdinImage(data []byte) (pub_models.ImagePart, error) {
	if image, err := c.userImage(data, "stdin"); !errors.Is(err, asset.ErrNotImage) {
		return image, err
	}
	trimmed := string(bytes.TrimSpace(data))
	decoded, err := base64.StdEncoding.Strict().DecodeString(trimmed)
	if err != nil || base64.StdEncoding.EncodeToString(decoded) != trimmed {
		return pub_models.ImagePart{}, asset.ErrNotImage
	}
	return c.userImage(decoded, "stdin")
}
