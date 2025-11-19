package chat

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"

	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

var ErrNoMIMEType = errors.New("failed to find mimetype")

// PromptToImageMessage by extracting b64 encoded images into becoming
// a message with ContentParts. If no b64 image is found in the prompt,
// the returned message is
func PromptToImageMessage(prompt string) ([]pub_models.Message, error) {
	b64Strings, parsedPrompt := extractB64Strings(prompt)
	if len(b64Strings) > 0 {
		imgOrTxt := []pub_models.ImageOrTextInput{}
		for _, b64 := range b64Strings {
			mime, err := detectB64MIME(b64)
			if err != nil {
				if errors.Is(err, ErrNoMIMEType) {
					// It's most likely a path, so simply skip it. It will make the
					// prompt a bit wonky
					ancli.Warnf("detected string without mimetype, which was falsely identified as image. Prompt will now be wonky, falsely identified substring: '%v'", b64)
					continue
				}
				return nil, fmt.Errorf("failed to detect b64 mime: %w", err)
			}
			imgURL := pub_models.ImageURL{
				URL:      fmt.Sprintf("data:%v;base64,%v", mime, b64),
				Detail:   "auto",
				MIMEType: mime,
				RawB64:   b64,
			}
			imgOrTxt = append(imgOrTxt, pub_models.ImageOrTextInput{
				Type:     "image_url",
				ImageB64: &imgURL,
			})
		}
		imgOrTxt = append(imgOrTxt, pub_models.ImageOrTextInput{
			Type: "text",
			Text: parsedPrompt,
		})
		return []pub_models.Message{{
			Role:         "user",
			ContentParts: imgOrTxt,
		}}, nil
	} else {
		return []pub_models.Message{{
			Role:    "user",
			Content: prompt,
		}}, nil
	}
}

// extractB64Strings by:
//
// Returning a slice of b64 encoded strings, and a parsed version
// of the prompt.
//
// Procedure:
//  1. Find all occurences of b64 encoded substrings in prompt
//  2. Adding the b64 substring to return string slice
//  3. Replacing the b64 substring with substring <IMG_NR>, where
//     NR is incremented for each image, starting from 0
//  4. Retu
func extractB64Strings(prompt string) ([]string, string) {
	// Match candidate base64 segments. We'll validate strictly in the replacer.
	re := regexp.MustCompile(`[A-Za-z0-9+/=]+`)
	out := []string{}
	idx := 0
	strict := base64.StdEncoding.Strict()

	parsed := re.ReplaceAllStringFunc(prompt, func(s string) string {
		// Quick filters to avoid obvious non-base64 tokens
		if len(s) < 256 || len(s)%4 != 0 {
			return s
		}
		// padding must be at the end and at most 2 characters
		pad := 0
		for i := len(s) - 1; i >= 0 && s[i] == '='; i-- {
			pad++
		}
		if pad > 2 {
			return s
		}
		if pad > 0 {
			if strings.ContainsRune(s[:len(s)-pad], '=') {
				return s
			}
		}
		// Attempt strict decode
		decoded, err := strict.DecodeString(s)
		if err != nil {
			return s
		}
		// Ensure canonical representation to reduce false positives
		if base64.StdEncoding.EncodeToString(decoded) != s {
			return s
		}

		out = append(out, s)
		repl := fmt.Sprintf("<IMG_%d>", idx)
		idx++
		return repl
	})

	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.Okf("len prompt: %v, am b64: %v, out: %v", len(parsed), len(out), out)
	}
	return out, parsed
}

func detectB64MIME(b64 string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "", fmt.Errorf("failed to decode b64 string: %w", err)
	}
	ct := http.DetectContentType(data)
	if strings.HasPrefix(ct, "image/") {
		switch ct {
		case "image/png",
			"image/jpeg",
			"image/gif",
			"image/webp":
			return ct, nil
		}
	}
	if len(data) >= 8 {
		if data[0] == 0x89 &&
			data[1] == 0x50 &&
			data[2] == 0x4E &&
			data[3] == 0x47 &&
			data[4] == 0x0D &&
			data[5] == 0x0A &&
			data[6] == 0x1A &&
			data[7] == 0x0A {
			return "image/png", nil
		}
	}
	if len(data) >= 3 {
		if data[0] == 0xFF &&
			data[1] == 0xD8 &&
			data[2] == 0xFF {
			return "image/jpeg", nil
		}
	}
	if len(data) >= 6 {
		if string(data[:6]) == "GIF87a" ||
			string(data[:6]) == "GIF89a" {
			return "image/gif", nil
		}
	}
	if len(data) >= 12 {
		if string(data[:4]) == "RIFF" &&
			string(data[8:12]) == "WEBP" {
			return "image/webp", nil
		}
	}
	return "", ErrNoMIMEType
}
