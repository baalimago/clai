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
	if s := FormatRemoteTrustPrompt(remote.TrustPrompt{Target: "cchub:ada/bot", Digest: "abc", Page: "https://x/"}); !strings.Contains(s, "Trust cchub:ada/bot? [y/N]") || !strings.Contains(s, "Read it: https://x/") {
		t.Fatalf("prompt text = %q", s)
	}
}

func TestFormatRemoteTrustPrompt_escapesControlCharacters(t *testing.T) {
	got := FormatRemoteTrustPrompt(remote.TrustPrompt{Target: "https://x.example/\u009b2J", Digest: "abc\x1b", Page: "https://x.example/\u202e"})
	for _, want := range []string{`fetched https://x.example/\u009b2J · sha256 abc\x1b`, `Read it: https://x.example/\u202e`, `Trust https://x.example/\u009b2J? [y/N] `} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt %q lacks %q", got, want)
		}
	}
}
