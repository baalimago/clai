package tools

import (
	"reflect"
	"testing"
)

func TestCmdBanTokenizeCommand(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{"empty input", "", []string{}},
		{"plain words", "git status", []string{"git", "status"}},
		{"collapsed whitespace", "git   status\n\t", []string{"git", "status"}},
		{"single-sided leading quote", "'git", []string{"git"}},
		{"single-sided trailing quote", "commit'", []string{"commit"}},
		{"quoted word keeps inner quote", `"it's"`, []string{"it's"}},
		{"independent quote stripping", `"x"'`, []string{`x"`}},
		{"quoted multi-word flattens", "sh -c 'git commit'", []string{"sh", "-c", "git", "commit"}},
		{"semicolon split", "git;echo", []string{"git", "echo"}},
		{"double ampersand split", "git&&echo", []string{"git", "echo"}},
		{"command substitution keeps dollar", "$(git log)", []string{"$", "git", "log"}},
		{"backtick quoted flattens", "`git log`", []string{"git", "log"}},
		{"pipe split", "echo x | rm -rf", []string{"echo", "x", "rm", "-rf"}},
		{"redirection metachars", "echo hi > out", []string{"echo", "hi", "out"}},
		{"metachar run drops empties", "git;;echo", []string{"git", "echo"}},
		{"only metachars", ";;", []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tokenizeCommand(tt.raw)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("tokenizeCommand(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestCmdBanMatch(t *testing.T) {
	tests := []struct {
		name        string
		command     string
		entries     []string
		wantMatched string
		wantBanned  bool
	}{
		{"nil entries", "rm -rf /", nil, "", false},
		{"empty entries", "rm -rf /", []string{}, "", false},
		{"empty command", "", []string{"rm"}, "", false},
		{"single-token direct", "rm -rf /", []string{"rm"}, "rm", true},
		{"single-token piped", "echo x | rm -rf", []string{"rm"}, "rm", true},
		{"single-token quoted shell", `sh -c "rm -rf /"`, []string{"rm"}, "rm", true},
		{"single-token nested", "docker exec c rm -f f", []string{"rm"}, "rm", true},
		{"no partial word match", "rmdir empty", []string{"rm"}, "", false},
		{"case sensitive", "RM -rf /", []string{"rm"}, "", false},
		{"multi-token direct", `git commit -m "x"`, []string{"git commit"}, "git commit", true},
		{"multi-token chained", "git stash && git commit", []string{"git commit"}, "git commit", true},
		{"multi-token quoted", "sh -c 'git commit'", []string{"git commit"}, "git commit", true},
		{"no match other subcommand", "git log", []string{"git commit"}, "", false},
		{"reversed order", "commit git", []string{"git commit"}, "", false},
		{"variable assignment not expanded", "x=git; $x commit", []string{"git commit"}, "", false},
		{"metachar-joined semicolon", "git;echo", []string{"git"}, "git", true},
		{"metachar-joined and", "git&&echo", []string{"git"}, "git", true},
		{"command substitution", "$(git log)", []string{"git"}, "git", true},
		{"backtick form", "`git log`", []string{"git"}, "git", true},
		{"deny by content", "echo git commit", []string{"git commit"}, "git commit", true},
		{"literal spelling evasion", "/bin/rm -rf /", []string{"rm"}, "", false},
		{"first match in list order", "git commit", []string{"git", "git commit"}, "git", true},
		{"later entry matches", "git commit", []string{"git log", "git commit"}, "git commit", true},
		{"entry longer than command", "ls", []string{"git commit"}, "", false},
		{"tokens not contiguous", "git x commit", []string{"git commit"}, "", false},
		{"quoted entry never matches", "sh -c 'git commit'", []string{"'git commit'"}, "", false},
		{"whitespace-only entry never matches", "git commit", []string{"   "}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotMatched, gotBanned := matchCmdBan(tt.command, tt.entries)
			if gotBanned != tt.wantBanned || gotMatched != tt.wantMatched {
				t.Fatalf("matchCmdBan(%q, %v) = (%q, %v), want (%q, %v)", tt.command, tt.entries, gotMatched, gotBanned, tt.wantMatched, tt.wantBanned)
			}
		})
	}
}
