package tools

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWebsiteTextTool(t *testing.T) {
	// Test successful case
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`                                                                                                                                                                               
<html>
        <body>
                <h1>Hello World</h1>
                <p>This is some text</p>
        </body>
</html>`))
	}))
	defer server.Close()

	input := Input{"url": server.URL}
	expected := "Hello World\nThis is some text\n"

	actual, err := WebsiteText.Call(input)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if actual != expected {
		t.Errorf("Expected %q, got %q", expected, actual)
	}

	// Test invalid URL
	input = Input{"url": "invalid"}
	_, err = WebsiteText.Call(input)
	if err == nil {
		t.Error("Expected error for invalid URL, got nil")
	}

	// Test invalid input type
	input = Input{"url": 123}
	_, err = WebsiteText.Call(input)
	if err == nil {
		t.Error("Expected error for invalid input type, got nil")
	}
}
