package text

import (
	"slices"
	"testing"

	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

func Test_filterMcpServersByProfile(t *testing.T) {
	tests := []struct {
		name     string
		files    []string
		userConf Configurations
		want     []string
	}{
		{
			name:  "No specific tools configured, return all files",
			files: []string{"server1.json", "server2.json"},
			userConf: Configurations{
				RequestedToolGlobs: []string{},
			},
			want: []string{"server1.json", "server2.json"},
		},
		{
			name:  "Specific tool matches one server",
			files: []string{"server1.json", "server2.json"},
			userConf: Configurations{
				RequestedToolGlobs: []string{"mcp_server1"},
			},
			want: []string{"server1.json"},
		},
		{
			name:  "Wildcard matches all mcp",
			files: []string{"server1.json", "server2.json"},
			userConf: Configurations{
				RequestedToolGlobs: []string{"mcp_*"},
			},
			want: []string{"server1.json", "server2.json"},
		},
		{
			name:  "Wildcard match on some servers",
			files: []string{"server1.json", "server2.json"},
			userConf: Configurations{
				RequestedToolGlobs: []string{"mcp_server1*"},
			},
			want: []string{"server1.json"},
		},
		{
			name:  "Match on server tool",
			files: []string{"server1.json", "server2.json"},
			userConf: Configurations{
				RequestedToolGlobs: []string{"mcp_server1_tool0"},
			},
			want: []string{"server1.json"},
		},
		{
			name:  "No match for any servers",
			files: []string{"server1.json", "server2.json"},
			userConf: Configurations{
				RequestedToolGlobs: []string{"mcp_server3"},
			},
			want: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ancli.Noticef("== test: %v\n", tt.name)
			got := filterMcpServersByProfile(tt.files, tt.userConf)
			if !slices.Equal(got, tt.want) {
				t.Errorf("want %v, got: %v", tt.want, got)
			}
		})
	}
}
