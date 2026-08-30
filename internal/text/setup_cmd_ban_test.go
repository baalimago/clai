package text

import (
	"slices"
	"testing"
)

func Test_setupCmdBanConfig_AppendsParsedFlagEntries(t *testing.T) {
	tests := []struct {
		name    string
		base    []string
		flagVal string
		want    []string
	}{
		{
			name:    "No flag leaves base unchanged",
			base:    []string{"rm"},
			flagVal: "",
			want:    []string{"rm"},
		},
		{
			name:    "Flag entries append to base",
			base:    []string{"rm"},
			flagVal: "sudo",
			want:    []string{"rm", "sudo"},
		},
		{
			name:    "Stray spaces are trimmed",
			base:    nil,
			flagVal: "rm, sudo",
			want:    []string{"rm", "sudo"},
		},
		{
			name:    "Empty segments are dropped",
			base:    nil,
			flagVal: "rm,,sudo",
			want:    []string{"rm", "sudo"},
		},
		{
			name:    "Only empty segments add nothing",
			base:    []string{"rm"},
			flagVal: ",,",
			want:    []string{"rm"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tConf := Configurations{CmdBan: slices.Clone(tt.base)}
			setupCmdBanConfig(&tConf, tt.flagVal)
			if !slices.Equal(tConf.CmdBan, tt.want) {
				t.Fatalf("expected CmdBan %v, got %v", tt.want, tConf.CmdBan)
			}
		})
	}
}
