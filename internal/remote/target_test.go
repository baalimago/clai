package remote

import (
	"errors"
	"strings"
	"testing"
)

const testDigest = "9f3c0000000000000000000000000000000000000000000000000000000ae1a0"

func TestParseTarget_valid(t *testing.T) {
	for _, tc := range []struct {
		in       string
		want     Target
		str      string
		base     string
		pinned   bool
		fetchURL string
	}{
		{
			in:       "hub:lorentz/trade-bot",
			want:     Target{Kind: KindHub, Handle: "lorentz", Name: "trade-bot"},
			str:      "hub:lorentz/trade-bot",
			base:     "hub:lorentz/trade-bot",
			fetchURL: "https://clai.chat/hub/lorentz/trade-bot/raw",
		},
		{
			in:       "hub:lorentz/trade-bot@3#sha256=" + strings.ToUpper(testDigest),
			want:     Target{Kind: KindHub, Handle: "lorentz", Name: "trade-bot", Version: 3, SHA256: testDigest},
			str:      "hub:lorentz/trade-bot@3",
			base:     "hub:lorentz/trade-bot",
			pinned:   true,
			fetchURL: "https://clai.chat/hub/lorentz/trade-bot/v/3/raw",
		},
		{
			in:       "gh:baalimago/sakfraga-researcher@v5",
			want:     Target{Kind: KindGitHub, Owner: "baalimago", Repo: "sakfraga-researcher", Path: "agent.json", Ref: "v5"},
			str:      "gh:baalimago/sakfraga-researcher@v5",
			base:     "gh:baalimago/sakfraga-researcher",
			pinned:   true,
			fetchURL: "https://raw.githubusercontent.com/baalimago/sakfraga-researcher/v5/agent.json",
		},
		{
			in:       "gh:baalimago/dotfiles/agents/review.json",
			want:     Target{Kind: KindGitHub, Owner: "baalimago", Repo: "dotfiles", Path: "agents/review.json"},
			str:      "gh:baalimago/dotfiles/agents/review.json",
			base:     "gh:baalimago/dotfiles/agents/review.json",
			fetchURL: "https://raw.githubusercontent.com/baalimago/dotfiles/HEAD/agents/review.json",
		},
		{
			in:       "https://example.com/agents/ops.json",
			want:     Target{Kind: KindHTTPS, URL: "https://example.com/agents/ops.json"},
			str:      "https://example.com/agents/ops.json",
			base:     "https://example.com/agents/ops.json",
			fetchURL: "https://example.com/agents/ops.json",
		},
	} {
		t.Run(tc.in, func(t *testing.T) {
			got, err := ParseTarget(tc.in)
			if err != nil {
				t.Fatalf("ParseTarget(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("ParseTarget(%q) = %+v, want %+v", tc.in, got, tc.want)
			}
			if got.String() != tc.str || got.Base() != tc.base || got.Pinned() != tc.pinned {
				t.Fatalf("String/Base/Pinned = %q %q %v, want %q %q %v", got.String(), got.Base(), got.Pinned(), tc.str, tc.base, tc.pinned)
			}
			if u := got.FetchURL(DefaultHubBase + "/"); u != tc.fetchURL {
				t.Fatalf("FetchURL = %q, want %q", u, tc.fetchURL)
			}
		})
	}
}

func TestParseTarget_refusals(t *testing.T) {
	for _, in := range []string{
		"hub:lorentz",
		"hub:Lorentz/trade-bot",
		"hub:l/trade-bot",
		"hub:lorentz/trade-bot@0",
		"hub:lorentz/trade-bot@03",
		"hub:lorentz/trade-bot@latest",
		"hub:lorentz/trade-bot#sha256=abc",
		"hub:lorentz/trade-bot#md5=" + testDigest,
		"gh:baalimago",
		"gh:baalimago/repo/../x",
		"gh:baalimago/repo//x",
		"gh:baalimago/repo@a..b",
		"gh:-bad/repo",
		"http://example.com/a.json",
		"https:///a.json",
		"https://user@example.com/a.json",
		"https://exa mple.com/%zz",
	} {
		t.Run(in, func(t *testing.T) {
			_, err := ParseTarget(in)
			if err == nil || errors.Is(err, ErrNotRemote) {
				t.Fatalf("ParseTarget(%q) err = %v, want a parse error", in, err)
			}
			if !strings.Contains(err.Error(), in) {
				t.Fatalf("error %q does not name the target", err)
			}
		})
	}
}

func TestParseTarget_localPathsAreNotRemote(t *testing.T) {
	for _, in := range []string{"./trade-bot.json", "/abs/hub:x.json", "trade-bot.json", "hubby.json", ""} {
		if IsRemote(in) {
			t.Fatalf("IsRemote(%q) = true, want false", in)
		}
		if _, err := ParseTarget(in); !errors.Is(err, ErrNotRemote) {
			t.Fatalf("ParseTarget(%q) err = %v, want ErrNotRemote", in, err)
		}
	}
}

func TestTarget_namesAndPages(t *testing.T) {
	for _, tc := range []struct {
		in, name, page, unpinned string
	}{
		{"hub:lorentz/trade-bot@3", "trade-bot", "https://dev.clai.chat/hub/lorentz/trade-bot/", "hub:lorentz/trade-bot"},
		{"gh:baalimago/sakfraga@v5", "sakfraga", "https://github.com/baalimago/sakfraga/blob/v5/agent.json", "gh:baalimago/sakfraga@v5"},
		{"gh:baalimago/dots/agents/review.json", "review", "https://github.com/baalimago/dots/blob/HEAD/agents/review.json", "gh:baalimago/dots/agents/review.json"},
		{"https://example.com/agents/ops.json", "ops", "https://example.com/agents/ops.json", "https://example.com/agents/ops.json"},
		{"https://example.com/", "remote", "https://example.com/", "https://example.com/"},
	} {
		got, err := ParseTarget(tc.in)
		if err != nil {
			t.Fatalf("ParseTarget(%q): %v", tc.in, err)
		}
		if got.DefaultName() != tc.name || got.PageURL("https://dev.clai.chat") != tc.page || got.Unpinned().String() != tc.unpinned {
			t.Fatalf("%q: name %q page %q unpinned %q", tc.in, got.DefaultName(), got.PageURL("https://dev.clai.chat"), got.Unpinned().String())
		}
	}
	got, _ := ParseTarget("hub:a1/b@2")
	if w := got.WithDigest(testDigest); w != "hub:a1/b@2#sha256="+testDigest {
		t.Fatalf("WithDigest = %q", w)
	}
	if KindHub.String() != "hub" || KindGitHub.String() != "gh" || KindHTTPS.String() != "https" || Kind(0).String() != "unknown" {
		t.Fatal("Kind.String")
	}
}
