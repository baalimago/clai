package tools

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"

	pub_models "github.com/baalimago/clai/pkg/text/models"
	"golang.org/x/net/html"
)

type WebsiteTextTool pub_models.Specification

var WebsiteText = WebsiteTextTool{
	Name:        "website_text",
	Description: "Get the text content of a website by stripping all non-text tags and trimming whitespace.",
	Inputs: &pub_models.InputSchema{
		Type: "object",
		Properties: map[string]pub_models.ParameterObject{
			"url": {
				Type:        "string",
				Description: "The URL of the website to retrieve the text content from.",
			},
		},
		Required: []string{"url"},
	},
}

func (w WebsiteTextTool) Call(input pub_models.Input) (string, error) {
	url, ok := input["url"].(string)
	if !ok {
		return "", fmt.Errorf("url must be a string")
	}
	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("failed to fetch website: %w", err)
	}
	defer resp.Body.Close()

	var text strings.Builder
	tokenizer := html.NewTokenizer(resp.Body)
	for {
		tt := tokenizer.Next()
		if tt == html.ErrorToken {
			if tokenizer.Err() == io.EOF {
				break
			}
			return "", fmt.Errorf("tokenizer error: %w", tokenizer.Err())
		}
		if tt == html.TextToken {
			trimmed := bytes.TrimSpace(tokenizer.Text())
			if len(trimmed) > 0 {
				text.Write(trimmed)
				text.WriteRune('\n')
			}
		}
	}
	return text.String(), nil
}

func (w WebsiteTextTool) Specification() pub_models.Specification {
	return pub_models.Specification(WebsiteText)
}
