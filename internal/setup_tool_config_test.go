package internal

import (
	"slices"
	"testing"

	"github.com/baalimago/clai/internal/text"
)

func Test_setupToolConfig_PopulatesRequestedToolGlobsFromFlags(t *testing.T) {
	tConf := text.Configurations{
		UseTools: false,
	}
	flagSet := Configurations{
		UseTools: "date",
	}

	setupToolConfig(&tConf, flagSet)

	if !tConf.UseTools {
		t.Fatalf("expected UseTools to be true when -t is provided")
	}
	want := []string{"date"}
	if !slices.Equal(tConf.RequestedToolGlobs, want) {
		t.Fatalf("expected RequestedToolGlobs %v, got %v", want, tConf.RequestedToolGlobs)
	}
}

func Test_setupToolConfig_MapsToolFlags(t *testing.T) {
	tests := []struct {
		name           string
		flagTools      string
		wantUseTools   bool
		wantToolGlobs  []string
		wantEmptyGlobs bool
	}{
		{
			name:           "All tools wildcard clears requested globs",
			flagTools:      "*",
			wantUseTools:   true,
			wantEmptyGlobs: true,
		},
		{
			name:          "MCP-prefixed tools are accepted as-is",
			flagTools:     "mcp_server1_tool0",
			wantUseTools:  true,
			wantToolGlobs: []string{"mcp_server1_tool0"},
		},
		{
			name:           "Unknown tool disables tools for the run",
			flagTools:      "does_not_exist",
			wantUseTools:   false,
			wantEmptyGlobs: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tConf := text.Configurations{
				UseTools: false,
			}
			flagSet := Configurations{
				UseTools: tc.flagTools,
			}

			setupToolConfig(&tConf, flagSet)

			if tConf.UseTools != tc.wantUseTools {
				t.Fatalf("expected UseTools=%v, got %v", tc.wantUseTools, tConf.UseTools)
			}
			if tc.wantEmptyGlobs {
				if len(tConf.RequestedToolGlobs) != 0 {
					t.Fatalf("expected RequestedToolGlobs to be empty, got %v", tConf.RequestedToolGlobs)
				}
				return
			}
			if !slices.Equal(tConf.RequestedToolGlobs, tc.wantToolGlobs) {
				t.Fatalf("expected RequestedToolGlobs %v, got %v", tc.wantToolGlobs, tConf.RequestedToolGlobs)
			}
		})
	}
}
