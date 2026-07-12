package agent

import (
	"testing"
)

func TestExtractJSONCandidates(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		wantLens []int
	}{
		{
			name:     "no braces",
			content:  "no json",
			wantLens: nil,
		},
		{
			name:     "single object",
			content:  `{"a":1}`,
			wantLens: []int{7},
		},
		{
			name:     "nested objects",
			content:  `{"outer":{"inner":1}}`,
			wantLens: []int{21, 11},
		},
		{
			name:     "thinking with brace tokens",
			content:  `Let me use {YYYY} and {MM} tokens. {"real":"json"}`,
			wantLens: []int{15, 6, 4},
		},
		{
			name:     "multiple top-level objects",
			content:  `{"first":1} {"second":2}`,
			wantLens: []int{12, 11},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidates := extractJSONCandidates(tt.content)
			if len(candidates) != len(tt.wantLens) {
				t.Fatalf("expected %d candidates, got %d: %v", len(tt.wantLens), len(candidates), candidates)
			}
			for i, want := range tt.wantLens {
				if len(candidates[i]) != want {
					t.Errorf("candidate[%d]: expected len %d, got %d (%q)", i, want, len(candidates[i]), candidates[i])
				}
			}
		})
	}
}

func TestParseTyped(t *testing.T) {
	type simple struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}

	t.Run("valid json", func(t *testing.T) {
		result, err := parseTyped[simple](`{"name":"test","value":42}`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Name != "test" || result.Value != 42 {
			t.Fatalf("unexpected result: %+v", result)
		}
	})

	t.Run("json with surrounding text", func(t *testing.T) {
		result, err := parseTyped[simple](`Here is the result: {"name":"test","value":42} and some trail`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Name != "test" {
			t.Fatalf("unexpected result: %+v", result)
		}
	})

	t.Run("thinking text with brace tokens", func(t *testing.T) {
		result, err := parseTyped[simple](`Using {YYYY} tokens. {"name":"test","value":42}`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Name != "test" {
			t.Fatalf("unexpected result: %+v", result)
		}
	})

	t.Run("no json", func(t *testing.T) {
		_, err := parseTyped[simple]("no json here")
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("invalid json in all candidates", func(t *testing.T) {
		_, err := parseTyped[simple](`{not valid} {also not}`)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("multiple candidates picks longest first", func(t *testing.T) {
		// Longest candidate {"name":"b","value":2} tried first, wins.
		result, err := parseTyped[simple](`{"name":"a"} {"name":"b","value":2}`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Name != "b" {
			t.Fatalf("expected longest candidate to win, got: %+v", result)
		}
	})
}
