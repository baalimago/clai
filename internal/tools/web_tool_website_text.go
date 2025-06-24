package tools

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"

	"golang.org/x/net/html"
)

type WebsiteTextTool Specification

var WebsiteText = WebsiteTextTool{
	Name:        "website_text",
	Description: "Get the text content of a website by stripping all non-text tags and trimming whitespace.",
	Inputs: InputSchema{
		Type: "object",
		Properties: map[string]ParameterObject{
			"url": {
				Type:        "string",
				Description: "The URL of the website to retrieve the text content from.",
			},
		},
		Required: []string{"url"},
	},
}

func (w WebsiteTextTool) Call(input Input) (string, error) {
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

func (w WebsiteTextTool) Specification() Specification {
	return Specification(WebsiteText)
}
