package tools

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestWebsiteTextTool_Simple(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(
				"<html><body>" +
					"<h1>Hello World</h1>" +
					"<p>This is some text</p>" +
					"</body></html>",
			))
		}))
	defer srv.Close()

	input := pub_models.Input{"url": srv.URL}
	exp := "Hello World\nThis is some text\n"

	got, err := WebsiteText.Call(input)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != exp {
		t.Fatalf("exp %q got %q", exp, got)
	}
}

func TestWebsiteTextTool_SkipAndBlocks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(
				"<html><head><title>t</title></head><body>" +
					"<h1>Hello</h1>" +
					"<script>var x=1</script>" +
					"<style>p{}</style>" +
					"<noscript>nope</noscript>" +
					"<iframe>ignored</iframe>" +
					"<p>Keep me</p>" +
					"</body></html>",
			))
		}))
	defer srv.Close()

	input := pub_models.Input{"url": srv.URL}
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
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(
				"<html><body>" +
					"Hello   \n\t  world<br>ok" +
					"</body></html>",
			))
		}))
	defer srv.Close()

	input := pub_models.Input{"url": srv.URL}
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
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set(
				"Content-Type",
				"text/html; charset=iso-8859-1",
			)
			w.Write([]byte(
				"<html><body><p>Caf\xe9</p></body></html>",
			))
		}))
	defer srv.Close()

	input := pub_models.Input{"url": srv.URL}
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
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte("nope"))
		}))
	defer srv.Close()

	input := pub_models.Input{"url": srv.URL}
	_, err := WebsiteText.Call(input)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestWebsiteTextTool_UnsupportedContentType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/pdf")
			w.Write([]byte("%PDF-1.7"))
		}))
	defer srv.Close()

	input := pub_models.Input{"url": srv.URL}
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
