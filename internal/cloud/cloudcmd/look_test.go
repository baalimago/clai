package cloudcmd

import (
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/cloud"
	"github.com/baalimago/clai/internal/cloud/sync"
)

func wantLines(t *testing.T, got []string, want ...string) {
	t.Helper()
	if g, w := strings.Join(got, "\n"), strings.Join(want, "\n"); g != w {
		t.Fatalf("got:\n%s\nwant:\n%s", g, w)
	}
}

func TestFirstPullLines_nameEachChoiceByWhatItKeeps(t *testing.T) {
	f := sync.FirstPull{Remote: 12, Local: 10, OnlyRemote: 3, Same: 8, Differ: []string{"profiles/x.json"}}
	wantLines(t, firstPullLines(sync.StoreConfig, f, style{}),
		"▸ config · first pull on this machine · 12 files on your account · 10 here",
		"  + 3 only on your account   pulled as they are",
		"  = 8 the same               nothing to do",
		"  ≠ 1 differs                profiles/x.json",
	)
	wantLines(t, firstPullChoices(sync.StoreConfig, 1, style{}),
		"  Keep which copy of the 1 file that differs?",
		"  [a] your account's   this machine's goes to cloud/trash/ for 30 days   same as pull -theirs",
		"  [h] this machine's   commit it later to share it                       same as pull -mine",
		"  [d] decide later     each file becomes a conflict to resolve",
		"  [q] quit             nothing is pulled",
	)
	wantLines(t, firstPullFlags(sync.StoreConfig, style{}),
		"  → clai cloud pull config -theirs   keep your account's copies",
		"  → clai cloud pull config -mine     keep this machine's copies",
	)
	many := sync.FirstPull{Differ: []string{"a.json", "b.json", "c.json", "d.json", "e.json"}}
	if got := strings.Join(firstPullLines(sync.StoreConfig, many, style{}), "\n"); !strings.Contains(got, "≠ 5 differ                 a.json, b.json, c.json, +2 more") {
		t.Fatalf("many differ:\n%s", got)
	}
}

func TestChoice_takesTheLetterOrTheWord(t *testing.T) {
	for in, want := range map[string]sync.Mode{"a": sync.ModeTheirs, "Account": sync.ModeTheirs, "h": sync.ModeMine, "here": sync.ModeMine, "d": sync.ModeConflicts, " later ": sync.ModeConflicts} {
		if got, ok := parseChoice(in); !ok || got.quit || got.mode != want {
			t.Errorf("%q = %+v %v, want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"q", "quit", " Q "} {
		if got, ok := parseChoice(in); !ok || !got.quit {
			t.Errorf("%q = %+v %v, want quit", in, got, ok)
		}
	}
	for _, in := range []string{"", "t", "x"} {
		if _, ok := parseChoice(in); ok {
			t.Errorf("%q was taken as a choice", in)
		}
	}
}

func TestConflictLines_showTheNextCommands(t *testing.T) {
	wantLines(t, conflictLines([]string{"profiles/x.json"}, style{}),
		"  C profiles/x.json   yours is untouched · your account's copy is cloud/theirs/profiles/x.json",
		"  → clai cloud diff profiles/x.json              compare the two",
		"  → clai cloud resolve profiles/x.json -theirs   take your account's copy",
		"  → clai cloud resolve profiles/x.json -mine     keep yours",
	)
	got := conflictLines([]string{"a.json", "b.json"}, style{})
	if !strings.Contains(strings.Join(got, "\n"), "→ clai cloud resolve <path> -theirs") {
		t.Fatalf("two conflicts:\n%s", strings.Join(got, "\n"))
	}
}

func TestPullLines(t *testing.T) {
	res := sync.PullResult{Store: sync.StoreConversations, Commit: 7, Added: 2, Trashed: []string{"x"}, Merged: []string{"m"}, Forks: []sync.Fork{{Path: "conversations/c1.json", Forked: "conversations/c2.json", Title: "hi (other)"}}, Conflicts: []string{"conversations/c3.json"}}
	got := strings.Join(pullLines(res, style{}), "\n")
	for _, want := range []string{
		"▸ conversations · pulled #7 · 2 added · 1 replaced · 1 merged · 1 forked · 1 conflict",
		"  ~ 1 merged   one copy only added messages to the other",
		`  ⑂ conversations/c1.json   continued on both machines · the other copy is now "hi (other)" (conversations/c2.json)`,
		"  replaced files are kept 30 days in cloud/trash/",
		"  C conversations/c3.json",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q:\n%s", want, got)
		}
	}
	wantLines(t, pullLines(sync.PullResult{Store: sync.StoreConfig, Commit: 2}, style{}), "▸ config · pulled #2 · up to date")
	wantLines(t, pullLines(sync.PullResult{Store: sync.StoreConversations}, style{}), "▸ conversations · up to date")
}

func TestCommitRefusalLines(t *testing.T) {
	wantLines(t, conflictsOpenLines(&sync.ConflictsOpenError{Store: sync.StoreConfig, Paths: []string{"a.json"}}, style{})[:1],
		"▸ config · not committed · 1 conflict to resolve first")
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	got := behindLines(&sync.BehindError{Store: sync.StoreConfig, Paths: []sync.RemoteEntry{{Path: "profiles/x.json", Commit: 2, Message: "edit", Device: "desk", UpdatedAt: now.Add(-2 * time.Hour).UnixMilli()}, {Path: "theme.json"}}}, now, style{})
	wantLines(t, got,
		"▸ config · not committed · your account has 2 newer files",
		`  ~ profiles/x.json   #2 "edit" · desk · 2 h ago`,
		"  ~ theme.json",
		"  → clai cloud pull config   then commit again",
	)
}

func TestAccountLines(t *testing.T) {
	wantLines(t, loginLines("@lorentz", "clai.chat", style{}),
		"▸ logged in · @lorentz on clai.chat · nothing syncs until you ask",
		"  → clai cloud status   what you would commit",
		"  → clai cloud pull     bring down your account's config",
		"  → clai cloud commit   upload your config",
	)
	wantLines(t, approveLines(cloud.DeviceCode{UserCode: "BCDF-GHJK", VerificationURIComplete: "https://clai.chat/cli/login#BCDF-GHJK"}, "laptop", style{}),
		`▸ log in · approve the code BCDF-GHJK for "laptop"`,
		"  → https://clai.chat/cli/login#BCDF-GHJK",
		"  waiting for approval (Ctrl-C to stop)",
	)
	me := cloud.Me{Handle: "lorentz", Device: "laptop", Access: "write", Stores: map[string]cloud.StoreInfo{"config": {Files: 30, Bytes: 12_000, Commit: 3}, "conversations": {Files: 5000, Bytes: 553_200_000, Commit: 1}}, Usage: cloud.Usage{ConfigLimitBytes: 1_000_000_000, CardOnFile: true}}
	wantLines(t, whoamiLines(me, "clai.chat", style{}),
		"▸ @lorentz · clai.chat · device laptop · write access",
		"  ■ config           30 files ·  12.0 kB · commit #3",
		"  ■ conversations  5.0k files · 553.2 MB · commit #1",
		"  billing: config sync is free (12.0 kB of 1.0 GB); conversations $1 a month at 553.2 MB",
	)
}

func TestLogAndResolveLines(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	wantLines(t, logLines(sync.StoreConfig, []sync.LogEntry{{Commit: 12, Message: "edit", Device: "desk", Files: 1, CreatedAt: now.UnixMilli()}, {Commit: 3, Device: "laptop", Files: 4, CreatedAt: now.Add(-3 * time.Hour).UnixMilli()}}, now, style{}),
		"▸ config · 2 commits",
		`  #12   "edit"   desk · 1 file · just now`,
		"  #3    -        laptop · 4 files · 3 h ago",
	)
	wantLines(t, logLines(sync.StoreConfig, nil, now, style{}), "▸ config · no commits yet")
	wantLines(t, resolveLines(sync.ResolveResult{Path: "a.json"}, resolvedTheirs, style{}), "▸ a.json · resolved · took your account's copy · yours is in cloud/trash/")
	wantLines(t, resolveLines(sync.ResolveResult{Path: "a.json"}, resolvedMine, style{}), "▸ a.json · resolved · kept yours", "  → clai cloud commit   share it")
	wantLines(t, resolveLines(sync.ResolveResult{Path: "a.json"}, resolvedEdited, style{}), "▸ a.json · resolved · kept your edited file", "  → clai cloud commit   share it")
}

func TestHuman_bytesBelowAKilobyte(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 31: "31 B", 999: "999 B", 1000: "1.0 kB", 1500: "1.5 kB", 3_000_000: "3.0 MB", 2_500_000_000: "2.5 GB"} {
		if got := human(n); got != want {
			t.Errorf("human(%d) = %q, want %q", n, got, want)
		}
	}
}
