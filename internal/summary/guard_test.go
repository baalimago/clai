package summary

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSummarizerGuard pins the operator kill switch: "off" refuses the
// real summarizer before it reads any config; anything else allows it.
func TestSummarizerGuard(t *testing.T) {
	cases := []struct {
		name    string
		env     string
		allowed bool
	}{
		{name: "unset allows", env: "", allowed: true},
		{name: "on allows", env: "on", allowed: true},
		{name: "off refuses", env: "off", allowed: false},
		{name: "OFF refuses", env: " OFF ", allowed: false},
		{name: "other values allow", env: "maybe", allowed: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(EnvSummarizer, tc.env)
			err := allowed()
			if (err == nil) != tc.allowed {
				t.Fatalf("allowed() = %v, want allowed=%v", err, tc.allowed)
			}
			if err != nil && !strings.Contains(err.Error(), EnvSummarizer) {
				t.Fatalf("refusal must name the switch, got %v", err)
			}
		})
	}
	t.Run("constructor refuses before reading any config", func(t *testing.T) {
		t.Setenv(EnvSummarizer, "off")
		confDir := t.TempDir()
		_, err := NewAgentSummarizer(confDir)
		if err == nil || !strings.Contains(err.Error(), EnvSummarizer) {
			t.Fatalf("NewAgentSummarizer err = %v, want the guard's refusal", err)
		}
		if _, statErr := os.Stat(filepath.Join(confDir, "textConfig.json")); !os.IsNotExist(statErr) {
			t.Fatalf("a refused constructor must not create textConfig.json, stat err = %v", statErr)
		}
	})
}
