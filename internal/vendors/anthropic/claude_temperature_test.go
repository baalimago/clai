package anthropic

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func Test_constructRequest_temperatureByModel(t *testing.T) {
	const asked = 0.5
	testCases := []struct {
		model     string
		wantTempr bool
	}{
		{"claude-sonnet-4", true},
		{"claude-sonnet-4-5", true},
		{"claude-opus-4-1-20250805", true},
		{"claude-sonnet-4-20250514", true},
		{"claude-3-5-sonnet-20241022", true},
		{"claude-3-opus-latest", true},
		{"claude-3-haiku-20240307", true},
		{"claude-sonnet-5-5", false},
		{"claude-opus-5", false},
		{"claude-haiku-5-20271001", false},
		{"claude-opus-7", false},
		{"claude-opus-10", false},
		{"claude-6-sonnet", false},
		{"anthropic/claude-opus-7", false},
		{"Claude-Opus-7", false},
		// A name without a recognisable version cannot be proven to accept
		// temperature; omitting it is always valid, sending it may be a 400.
		{"claude-next", false},
		{"", false},
	}
	for _, tc := range testCases {
		t.Run(tc.model, func(t *testing.T) {
			c := &Claude{Model: tc.model, Temperature: asked, URL: "http://localhost/v1/messages"}
			req, err := c.constructRequest(context.Background(), pub_models.Chat{
				Messages: []pub_models.Message{{Role: "user", Content: "hi"}},
			})
			if err != nil {
				t.Fatalf("constructRequest: %v", err)
			}
			body, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			var got map[string]any
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("unmarshal body: %v", err)
			}
			temp, present := got["temperature"]
			if present != tc.wantTempr {
				t.Fatalf("model %q: temperature present=%v, want %v (body: %s)", tc.model, present, tc.wantTempr, body)
			}
			if present && temp != asked {
				t.Fatalf("model %q: temperature=%v, want %v", tc.model, temp, asked)
			}
		})
	}
}
