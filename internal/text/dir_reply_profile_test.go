package text

import (
	"slices"
	"testing"
)

// A chat run with a remote profile is stamped with its target; replying to
// it fetches that target again instead of looking for a local profile of
// that name.
func TestProfileArgs_remoteTargetIsFetched(t *testing.T) {
	for profile, want := range map[string][]string{
		"":                      nil,
		"bot":                   {"-p", "bot"},
		"cchub:clai/reviewer@5": {"-prp", "cchub:clai/reviewer@5"},
		"gh:ada/kit/bot.json":   {"-prp", "gh:ada/kit/bot.json"},
	} {
		if got := profileArgs(profile); !slices.Equal(got, want) {
			t.Errorf("profileArgs(%q) = %q, want %q", profile, got, want)
		}
	}
}
