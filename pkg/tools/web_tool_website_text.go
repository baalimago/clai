package tools

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
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

type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

var websiteTextHTTPClient httpDoer = &http.Client{Timeout: 10 * time.Second}

func (w WebsiteTextTool) Call(input pub_models.Input) (string, error) {
	urlStr, ok := input["url"].(string)
	if !ok {
		return "", fmt.Errorf("url must be a string")
	}

	u, err := url.ParseRequestURI(urlStr)
	if err != nil {
		return "", fmt.Errorf("invalid url: %w", err)
	}

	ua := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) " +
		"AppleWebKit/537.36 (KHTML, like Gecko) " +
		"Chrome/124.0.0.0 Safari/537.36"

	client := websiteTextHTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept",
		"text/html,application/xhtml+xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("bad status: %s", resp.Status)
	}

	ctype := resp.Header.Get("Content-Type")
	if ctype != "" &&
		!strings.Contains(ctype, "text/html") &&
		!strings.Contains(ctype, "application/xhtml+xml") &&
		!strings.Contains(ctype, "text/plain") {
		return "", fmt.Errorf("unsupported content-type: %s", ctype)
	}

	var r io.Reader = resp.Body
	r = io.LimitReader(r, 5<<20)

	ur, err := charset.NewReader(r, ctype)
	if err != nil {
		ur = r
	}

	tokenizer := html.NewTokenizer(ur)

	skipTags := map[string]bool{
		"script":   true,
		"style":    true,
		"noscript": true,
		"head":     true,
		"iframe":   true,
		"svg":      true,
		"canvas":   true,
		"template": true,
	}
	blockTags := map[string]bool{
		"p":       true,
		"div":     true,
		"li":      true,
		"section": true,
		"article": true,
		"h1":      true,
		"h2":      true,
		"h3":      true,
		"h4":      true,
		"h5":      true,
		"h6":      true,
		"header":  true,
		"footer":  true,
		"nav":     true,
		"br":      true,
		"ul":      true,
		"ol":      true,
	}

	skipDepth := 0
	var text strings.Builder

	writeNL := func() {
		if text.Len() == 0 {
			return
		}
		s := text.String()
		if len(s) > 0 && s[len(s)-1] != '\n' {
			text.WriteByte('\n')
		}
	}

	for {
		tt := tokenizer.Next()
		if tt == html.ErrorToken {
			if tokenizer.Err() == io.EOF {
				break
			}
			return "", fmt.Errorf("tokenizer error: %w",
				tokenizer.Err())
		}
		switch tt {
		case html.StartTagToken:
			t := tokenizer.Token()
			name := strings.ToLower(t.Data)
			if skipTags[name] {
				skipDepth++
			}
			if blockTags[name] {
				writeNL()
			}
		case html.EndTagToken:
			t := tokenizer.Token()
			name := strings.ToLower(t.Data)
			if skipTags[name] && skipDepth > 0 {
				skipDepth--
			}
			if blockTags[name] {
				writeNL()
			}
		case html.TextToken:
			if skipDepth > 0 {
				continue
			}
			b := bytes.TrimSpace(tokenizer.Text())
			if len(b) == 0 {
				continue
			}
			fields := bytes.Fields(b)
			if len(fields) == 0 {
				continue
			}
			for i, f := range fields {
				if i > 0 {
					text.WriteByte(' ')
				}
				text.Write(f)
			}
			text.WriteByte('\n')
		}
	}

	out := strings.TrimSpace(text.String())
	if out == "" {
		return "", nil
	}
	for strings.Contains(out, "\n\n\n") {
		out = strings.ReplaceAll(out, "\n\n\n", "\n\n")
	}
	return out + "\n", nil
}

func (w WebsiteTextTool) Specification() pub_models.Specification {
	return pub_models.Specification(WebsiteText)
}
