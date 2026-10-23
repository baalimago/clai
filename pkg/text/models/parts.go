package models

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// HandleLength is the README parameter "Handle length": the hex digits of a handle.
const HandleLength = 6

// Describer renders content for surfaces that show only text.
type Describer interface {
	// Describe returns the content as plain text.
	//  1. A TextPart describes as its Text, byte for byte.
	//  2. An ImagePart describes as "[image <handle>]" (I1). An ImagePart whose
	//     handle cannot be derived describes as "[image ?]".
	Describe() string
}

// Part is one piece of message content. The set is closed: TextPart and ImagePart.
type Part interface {
	Describer
	isPart()
}

type TextPart struct{ Text string }

func (t TextPart) Describe() string { return t.Text }
func (TextPart) isPart()            {}

type ImageOrigin string

const (
	OriginUser      ImageOrigin = "user"
	OriginTool      ImageOrigin = "tool"
	OriginGenerated ImageOrigin = "generated"
)

// ImagePart is an image, either stored (Asset set) or pending (Data set).
type ImagePart struct {
	Asset  string // "<sha256-hex>.<ext>"; empty while pending
	MIME   string // one of the README's MIME-to-extension keys
	Origin ImageOrigin
	Data   []byte // bytes of a pending image; never persisted (I5)
}

func (p ImagePart) Describe() string {
	h, err := p.Handle()
	if err != nil {
		return "[image ?]"
	}
	return "[image " + h + "]"
}

func (ImagePart) isPart() {}

// Handle returns the image's handle.
//  1. A stored image returns the first handle-length characters of Asset.
//  2. A pending image returns the first handle-length hex digits of sha256(Data),
//     so the same bytes give the same handle once stored (I1).
//  3. An image with neither Asset nor Data returns ErrImageEmpty.
func (p ImagePart) Handle() (string, error) {
	switch {
	case p.Asset != "":
		if len(p.Asset) < HandleLength {
			return "", fmt.Errorf("asset ref %q is shorter than a %d-digit handle", p.Asset, HandleLength)
		}
		return p.Asset[:HandleLength], nil
	case len(p.Data) > 0:
		sum := sha256.Sum256(p.Data)
		return hex.EncodeToString(sum[:])[:HandleLength], nil
	default:
		return "", ErrImageEmpty
	}
}

var (
	ErrImageEmpty      = errors.New("image part has neither asset nor data")
	ErrPendingImage    = errors.New("pending image cannot be persisted")
	ErrUnknownPartType = errors.New("unknown content part type")
	ErrLegacyImageURL  = errors.New("legacy image part is not a base64 data URL")
)

type textPartJSON struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type imagePartJSON struct {
	Type   string      `json:"type"`
	Asset  string      `json:"asset"`
	MIME   string      `json:"mime"`
	Origin ImageOrigin `json:"origin"`
}

const legacyImageType = "image_url"

type legacyImageURL struct {
	URL string `json:"url"`
}

type partJSON struct {
	Type   string          `json:"type"`
	Text   string          `json:"text"`
	Asset  string          `json:"asset"`
	MIME   string          `json:"mime"`
	Origin ImageOrigin     `json:"origin"`
	Legacy *legacyImageURL `json:"image_url"`
}

func encodeParts(parts []Part) ([]any, error) {
	out := make([]any, 0, len(parts))
	for i, part := range parts {
		switch p := part.(type) {
		case TextPart:
			out = append(out, textPartJSON{Type: "text", Text: p.Text})
		case ImagePart:
			if p.Asset == "" {
				if len(p.Data) > 0 {
					return nil, fmt.Errorf("content part %d: %w", i, ErrPendingImage)
				}
				return nil, fmt.Errorf("content part %d: %w", i, ErrImageEmpty)
			}
			out = append(out, imagePartJSON{Type: "image", Asset: p.Asset, MIME: p.MIME, Origin: p.Origin})
		default:
			return nil, fmt.Errorf("content part %d: %w: %T", i, ErrUnknownPartType, part)
		}
	}
	return out, nil
}

func decodeParts(raw []partJSON) ([]Part, error) {
	out := make([]Part, 0, len(raw))
	for i, el := range raw {
		switch el.Type {
		case "text":
			out = append(out, TextPart{Text: el.Text})
		case "image":
			if el.Asset == "" {
				return nil, fmt.Errorf("content part %d: %w", i, ErrImageEmpty)
			}
			out = append(out, ImagePart{Asset: el.Asset, MIME: el.MIME, Origin: el.Origin})
		case legacyImageType:
			p, err := decodeLegacyImage(el.Legacy)
			if err != nil {
				return nil, fmt.Errorf("content part %d: %w", i, err)
			}
			out = append(out, p)
		default:
			return nil, fmt.Errorf("content part %d: %w: %q", i, ErrUnknownPartType, el.Type)
		}
	}
	return out, nil
}

func decodeLegacyImage(u *legacyImageURL) (ImagePart, error) {
	if u == nil {
		return ImagePart{}, fmt.Errorf("%w: image_url is missing", ErrLegacyImageURL)
	}
	rest, ok := strings.CutPrefix(u.URL, "data:")
	if !ok {
		return ImagePart{}, fmt.Errorf("%w: url is not a data URL", ErrLegacyImageURL)
	}
	header, payload, ok := strings.Cut(rest, ",")
	if !ok || !strings.HasSuffix(header, ";base64") {
		return ImagePart{}, fmt.Errorf("%w: data URL is not base64", ErrLegacyImageURL)
	}
	mime, _, _ := strings.Cut(header, ";")
	if mime == "" {
		return ImagePart{}, fmt.Errorf("%w: data URL has no MIME type", ErrLegacyImageURL)
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return ImagePart{}, fmt.Errorf("%w: %w", ErrLegacyImageURL, err)
	}
	if len(data) == 0 {
		return ImagePart{}, fmt.Errorf("%w: data URL holds no bytes", ErrLegacyImageURL)
	}
	return ImagePart{Origin: OriginUser, MIME: mime, Data: data}, nil
}
