package text

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/look"
	"github.com/baalimago/clai/internal/remote"
)

func TestRemoteProfileKeys_matchProfile(t *testing.T) {
	typ := reflect.TypeFor[Profile]()
	var keys []string
	for field := range typ.Fields() {
		keys = append(keys, strings.Split(field.Tag.Get("json"), ",")[0])
	}
	want := slices.Clone(remote.ProfileKeys)
	slices.Sort(keys)
	slices.Sort(want)
	if !slices.Equal(keys, want) {
		t.Fatalf("Profile json keys = %v, remote.ProfileKeys = %v", keys, want)
	}
}

func TestResolveRemoteProfilePath(t *testing.T) {
	local := &Configurations{ProfilePath: "./bot.json"}
	if err := resolveRemoteProfilePath(t.Context(), local, nil); err != nil || local.ProfilePath != "./bot.json" {
		t.Fatalf("local path: %v %q", err, local.ProfilePath)
	}
	if err := resolveRemoteProfilePath(t.Context(), &Configurations{ProfilePath: "cchub:ada/bot"}, nil); err == nil {
		t.Fatal("no resolver: want error")
	}
	c := &Configurations{ProfilePath: "cchub:ada/bot"}
	ok := func(context.Context, string) (string, error) { return "/cache/x.json", nil }
	if err := resolveRemoteProfilePath(t.Context(), c, ok); err != nil || c.ProfilePath != "/cache/x.json" {
		t.Fatalf("resolved: %v %q", err, c.ProfilePath)
	}
	refused := &remote.UntrustedError{Target: "cchub:ada/bot"}
	bad := func(context.Context, string) (string, error) { return "", refused }
	err := resolveRemoteProfilePath(t.Context(), &Configurations{ProfilePath: "cchub:ada/bot"}, bad)
	var untrusted *remote.UntrustedError
	if !errors.As(err, &untrusted) {
		t.Fatalf("err = %v, want the resolver's error wrapped", err)
	}
}

func TestRemoteProfileResolver(t *testing.T) {
	if _, err := remoteProfileResolver(nil, "", nil, false)(t.Context(), "cchub:ada/bot"); err == nil {
		t.Fatal("nil factory: want error")
	}
	failing := func(string, remote.Prompter) (remote.Resolver, error) {
		return remote.Resolver{}, errors.New("no cache")
	}
	if _, err := remoteProfileResolver(failing, "", nil, false)(t.Context(), "cchub:ada/bot"); err == nil || err.Error() != "no cache" {
		t.Fatalf("factory error: %v", err)
	}
	var gotPrompt remote.Prompter
	capture := func(_ string, p remote.Prompter) (remote.Resolver, error) {
		gotPrompt = p
		return remote.Resolver{}, errors.New("stop")
	}
	_, _ = remoteProfileResolver(capture, "", strings.NewReader("y\n"), true)(t.Context(), "cchub:ada/bot")
	if gotPrompt == nil {
		t.Fatal("interactive with input: want a prompter")
	}
	ok, err := gotPrompt(t.Context(), remote.TrustPrompt{Target: "cchub:ada/bot", Digest: "d"})
	if err != nil || !ok {
		t.Fatalf("prompt answered y: %v %v", ok, err)
	}
	_, _ = remoteProfileResolver(capture, "", strings.NewReader("y\n"), false)(t.Context(), "cchub:ada/bot")
	t.Setenv("TTY", filepath.Join(t.TempDir(), "no-tty"))
	if terminalAvailable() {
		t.Fatal("terminalAvailable with no openable TTY = true")
	}
	t.Setenv("TTY", os.DevNull)
	if terminalAvailable() {
		t.Fatal("terminalAvailable with TTY=/dev/null = true")
	}
	if gotPrompt != nil {
		t.Fatal("non-interactive: want no prompter")
	}
	var shown strings.Builder
	no := terminalTrustPrompt(strings.NewReader("\n"), &shown)
	if ok, err := no(t.Context(), remote.TrustPrompt{Target: "cchub:ada/bot"}); err != nil || ok {
		t.Fatalf("empty answer: %v %v", ok, err)
	}
	if !strings.Contains(shown.String(), "Trust cchub:ada/bot? [y/N]") {
		t.Fatalf("prompt shown = %q", shown.String())
	}
	if _, err := terminalTrustPrompt(strings.NewReader(""), io.Discard)(t.Context(), remote.TrustPrompt{}); err == nil {
		t.Fatal("closed input: want error")
	}
	if s := FormatRemoteTrustPrompt(remote.TrustPrompt{Target: "cchub:ada/bot", Digest: "abc", Page: "https://x/"}, look.Style{}); !strings.Contains(s, "\n\n? Trust cchub:ada/bot? [y/N] ") || !strings.Contains(s, "  page     https://x/\n") {
		t.Fatalf("prompt text = %q", s)
	}
}

func TestFormatRemoteTrustPrompt_escapesControlCharacters(t *testing.T) {
	got := FormatRemoteTrustPrompt(remote.TrustPrompt{Target: "https://x.example/\u009b2J", Digest: "abc\x1b", Page: "https://x.example/\u202e"}, look.Style{})
	for _, want := range []string{`▸ https://x.example/\u009b2J · fetched`, `sha256   abc\x1b`, `page     https://x.example/\u202e`, `Trust https://x.example/\u009b2J? [y/N] `} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt %q lacks %q", got, want)
		}
	}
}

func TestFormatRemoteTrustPrompt_sectionsInClaisLook(t *testing.T) {
	summary := remote.Summary{
		Model:  "gpt-6.1-sol",
		Access: remote.ToolsListed,
		Tools:  []string{"date", "website_text", "mcp_memory*", "mcp_polymarket*"},
		Servers: []remote.Server{
			{Name: "memory", Runs: "npx -y @modelcontextprotocol/server-memory@2.0.0", Env: []string{"MEMORY_FILE_PATH=~/.trade-bot.jsonl"}},
			{Name: "polymarket", Runs: "npx -y @iqai/mcp-polymarket@0.1.0"},
		},
		Prompt: "You are an automated trading agent.\nTrade small.",
	}
	got := FormatRemoteTrustPrompt(remote.TrustPrompt{Target: "cchub:clai/trade-bot@1", Digest: "6934c5", Page: "https://www.clai.chat/hub/clai/trade-bot/", Summary: summary}, look.Style{})
	want := `▸ cchub:clai/trade-bot@1 · fetched · runs on your machine
  sha256   6934c5
  page     https://www.clai.chat/hub/clai/trade-bot/

▸ MCP servers · 2
  memory       npx -y @modelcontextprotocol/server-memory@2.0.0
               env MEMORY_FILE_PATH=~/.trade-bot.jsonl
  polymarket   npx -y @iqai/mcp-polymarket@0.1.0
▸ tools · date, website_text, mcp_memory*, mcp_polymarket*
▸ model · gpt-6.1-sol
▸ prompt · 48 characters
  │ You are an automated trading agent.
  │ Trade small.

? Trust cchub:clai/trade-bot@1? [y/N] `
	if got != want {
		t.Fatalf("prompt =\n%s\nwant\n%s", got, want)
	}
}

func TestFormatRemoteTrustPrompt_paintsTheQuestion(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	got := FormatRemoteTrustPrompt(remote.TrustPrompt{Target: "cchub:ada/bot", Digest: "d"}, look.Style{Color: true})
	question := got[strings.LastIndex(got, "\n")+1:]
	if !strings.HasPrefix(question, "\x1b[") || !strings.HasSuffix(question, " [y/N] ") {
		t.Fatalf("question line = %q, want it painted", question)
	}
}
