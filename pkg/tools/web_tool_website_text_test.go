package tools

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func withWebsiteTextClient(t *testing.T, rt http.RoundTripper) {
	t.Helper()
	prev := websiteTextHTTPClient
	t.Cleanup(func() {
		websiteTextHTTPClient = prev
	})
	websiteTextHTTPClient = &http.Client{Transport: rt}
}

func stubResponse(
	t *testing.T,
	statusCode int,
	header map[string]string,
	body string,
) *http.Response {
	t.Helper()
	h := make(http.Header, len(header))
	for k, v := range header {
		h.Set(k, v)
	}
	return &http.Response{
		StatusCode: statusCode,
		Status:     fmt.Sprintf("%d %s", statusCode, http.StatusText(statusCode)),
		Header:     h,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestWebsiteTextTool_Simple(t *testing.T) {
	withWebsiteTextClient(t, roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		body := "<html><body>" +
			"<h1>Hello World</h1>" +
			"<p>This is some text</p>" +
			"</body></html>"
		resp := stubResponse(t, http.StatusOK, nil, body)
		resp.Request = r
		return resp, nil
	}))

	input := pub_models.Input{"url": "http://example.test"}
	exp := "Hello World\nThis is some text\n"

	got, err := WebsiteText.Call(input)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != exp {
		t.Fatalf("exp %q got %q", exp, got)
	}
}

func TestWebsiteTextTool_ReturnsSupportedNonHTMLFormats(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
	}{
		{
			name:        "json",
			contentType: "application/json; charset=utf-8",
			body:        "{\n  \"message\": \"hello\"\n}\n",
		},
		{
			name:        "vendor json",
			contentType: "application/vnd.example+json",
			body:        "{\"message\":\"hello\"}",
		},
		{
			name:        "markdown",
			contentType: "text/markdown; charset=utf-8",
			body:        "# Hello\n\n**world**\n",
		},
		{
			name:        "plain text",
			contentType: "text/plain",
			body:        "Hello\n\nworld\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withWebsiteTextClient(t, roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				resp := stubResponse(t, http.StatusOK, map[string]string{
					"Content-Type": tt.contentType,
				}, tt.body)
				resp.Request = r
				return resp, nil
			}))

			got, err := WebsiteText.Call(pub_models.Input{"url": "http://example.test"})
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got != tt.body {
				t.Fatalf("expected response body %q, got %q", tt.body, got)
			}
		})
	}
}

func TestWebsiteTextTool_PrefersMarkdownThenJSONThenHTML(t *testing.T) {
	withWebsiteTextClient(t, roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		accept := r.Header.Get("Accept")
		markdownIndex := strings.Index(accept, "text/markdown")
		jsonIndex := strings.Index(accept, "application/json")
		htmlIndex := strings.Index(accept, "text/html")
		if markdownIndex < 0 || jsonIndex < 0 || htmlIndex < 0 ||
			!(markdownIndex < jsonIndex && jsonIndex < htmlIndex) {
			t.Errorf("Accept header should prefer markdown, then JSON, then HTML; got %q", accept)
		}
		if got := r.Header.Get("Accept-Encoding"); got != "" {
			t.Errorf("expected no explicit Accept-Encoding header, got %q", got)
		}

		resp := stubResponse(t, http.StatusOK, map[string]string{
			"Content-Type": "text/plain",
		}, "ok")
		resp.Request = r
		return resp, nil
	}))

	if _, err := WebsiteText.Call(pub_models.Input{"url": "http://example.test"}); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestWebsiteTextTool_SkipAndBlocks(t *testing.T) {
	withWebsiteTextClient(t, roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		body := "<html><head><title>t</title></head><body>" +
			"<h1>Hello</h1>" +
			"<script>var x=1</script>" +
			"<style>p{}</style>" +
			"<noscript>nope</noscript>" +
			"<iframe>ignored</iframe>" +
			"<p>Keep me</p>" +
			"</body></html>"
		resp := stubResponse(t, http.StatusOK, map[string]string{
			"Content-Type": "text/html",
		}, body)
		resp.Request = r
		return resp, nil
	}))

	input := pub_models.Input{"url": "http://example.test"}
	exp := "Hello\nKeep me\n"

	got, err := WebsiteText.Call(input)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != exp {
		t.Fatalf("exp %q got %q", exp, got)
	}
}

func TestWebsiteTextTool_BRAndWhitespace(t *testing.T) {
	withWebsiteTextClient(t, roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		body := "<html><body>" +
			"Hello   \n\t  world<br>ok" +
			"</body></html>"
		resp := stubResponse(t, http.StatusOK, map[string]string{
			"Content-Type": "text/html",
		}, body)
		resp.Request = r
		return resp, nil
	}))

	input := pub_models.Input{"url": "http://example.test"}
	exp := "Hello world\nok\n"

	got, err := WebsiteText.Call(input)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != exp {
		t.Fatalf("exp %q got %q", exp, got)
	}
}

func TestWebsiteTextTool_CharsetDecoding(t *testing.T) {
	withWebsiteTextClient(t, roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		body := "<html><body><p>Caf\xe9</p></body></html>"
		resp := stubResponse(t, http.StatusOK, map[string]string{
			"Content-Type": "text/html; charset=iso-8859-1",
		}, body)
		resp.Request = r
		return resp, nil
	}))

	input := pub_models.Input{"url": "http://example.test"}
	exp := "Café\n"

	got, err := WebsiteText.Call(input)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != exp {
		t.Fatalf("exp %q got %q", exp, got)
	}
}

func TestWebsiteTextTool_BadStatus(t *testing.T) {
	withWebsiteTextClient(t, roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		resp := stubResponse(t, http.StatusForbidden, nil, "nope")
		resp.Request = r
		return resp, nil
	}))

	input := pub_models.Input{"url": "http://example.test"}
	_, err := WebsiteText.Call(input)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestWebsiteTextTool_UnsupportedContentType(t *testing.T) {
	withWebsiteTextClient(t, roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		resp := stubResponse(t, http.StatusOK, map[string]string{
			"Content-Type": "application/pdf",
		}, "%PDF-1.7")
		resp.Request = r
		return resp, nil
	}))

	input := pub_models.Input{"url": "http://example.test"}
	_, err := WebsiteText.Call(input)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestWebsiteTextTool_InvalidURL(t *testing.T) {
	input := pub_models.Input{"url": "invalid"}
	_, err := WebsiteText.Call(input)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestWebsiteTextTool_InvalidInputType(t *testing.T) {
	input := pub_models.Input{"url": 123}
	_, err := WebsiteText.Call(input)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
