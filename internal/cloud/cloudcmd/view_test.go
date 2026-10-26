package cloudcmd

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/cloud/sync"
	"github.com/baalimago/clai/internal/utils"
)

func plainLines(lines []string) string { return strings.Join(lines, "\n") }

func TestStatusLines_clean(t *testing.T) {
	st := sync.Status{Plan: sync.Plan{Store: sync.StoreConfig, Pulled: true, Synced: 30}, Remote: &sync.RemoteStatus{}}
	got := plainLines(statusLines(st, style{}))
	if got != "▸ config · up to date · 30 synced" {
		t.Fatalf("got %q", got)
	}
}

func TestStatusLines_fewChangesAreListed(t *testing.T) {
	st := sync.Status{
		Plan: sync.Plan{
			Store: sync.StoreConfig,
			Changes: []sync.LocalChange{
				{Path: "profiles/coder.json", Kind: sync.Added, Size: 1200},
				{Path: "theme.json", Kind: sync.Modified, Size: 300},
			},
			Conflicts: []string{"mcpServers/x.json"},
			Excluded:  []sync.Excluded{{Path: "profiles/leak.json", Kind: sync.ExcludedSecret, Detail: "api_key"}},
		},
		Remote: &sync.RemoteStatus{Ahead: 2, Both: []string{"theme.json"}, Uploaded: []string{"profiles/coder.json"}},
	}
	got := plainLines(statusLines(st, style{}))
	for _, want := range []string{
		"▸ config · 2 to commit · 1.5 kB · 1 conflict · server 2 commits ahead · 1 already uploaded · never pulled here",
		"  C mcpServers/x.json",
		"  → clai cloud resolve mcpServers/x.json -theirs",
		"  + profiles/coder.json  1.2 kB · uploaded, not committed",
		"  ~ theme.json  0.3 kB · also changed on the server",
		"  not synced, holds a secret: profiles/leak.json  api_key",
		"    move the value to an env var or envfile, then commit",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "share") {
		t.Errorf("a short list has no table:\n%s", got)
	}
}

func manyConversations() sync.Status {
	var changes []sync.LocalChange
	for i := range 40 {
		changes = append(changes, sync.LocalChange{Path: fmt.Sprintf("conversations/chat-%02d.json", i), Kind: sync.Added, Size: int64(1000 * (i + 1))})
	}
	changes = append(changes,
		sync.LocalChange{Path: "conversations/assets/" + strings.Repeat("a", 64) + ".png", Kind: sync.Added, Size: 5_000_000},
		sync.LocalChange{Path: "conversations/reasoning/chat-01/r1.json", Kind: sync.Modified, Size: 2_000},
		sync.LocalChange{Path: "conversations/old.json", Kind: sync.Deleted},
	)
	for _, tool := range []string{"claude-code", "codex", "gemini", "opencode", "cursor"} {
		changes = append(changes, sync.LocalChange{Path: "conversations/imported/" + tool + "/s.json", Kind: sync.Added, Size: 10})
	}
	return sync.Status{
		Plan:   sync.Plan{Store: sync.StoreConversations, Pulled: true, Synced: 12, Changes: changes, Excluded: []sync.Excluded{{Path: "conversations/imported/x/y.json", Kind: sync.ExcludedImport}}},
		Remote: &sync.RemoteStatus{Both: []string{"conversations/chat-01.json"}, Uploaded: []string{"conversations/chat-00.json", "conversations/chat-01.json", "conversations/chat-02.json"}},
	}
}

func TestStatusLines_manyChangesAreGrouped(t *testing.T) {
	lines := statusLines(manyConversations(), style{})
	got := plainLines(lines)
	for _, want := range []string{
		"▸ conversations · 48 to commit · 5.8 MB · 3 already uploaded · 1 changed on both sides · 12 synced",
		"uploaded",
		"  46 new · 1 edited · 1 deleted",
		"group",
		"files",
		"share",
		"largest",
		"■ images",
		"■ chats",
		"chat-39 40.0 kB · chat-38 39.0 kB · +39 more",
		"aaaaaaaaaaaaaaa… 5.0 MB",
		"■ reasoning",
		"■ +3 smaller",
		"  1 imported file from other tools left out; add them with commit -imports",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "chat-00.json") {
		t.Errorf("a long list should be grouped, not listed:\n%s", got)
	}
	if strings.Index(got, "■ images") > strings.Index(got, "■ chats") {
		t.Errorf("groups are ordered by size:\n%s", got)
	}
	rows := 0
	for _, l := range lines {
		if strings.Contains(l, "■") {
			rows++
		}
		if n := len([]rune(l)); n > lineWidth {
			t.Errorf("line of %d runes exceeds %d: %q", n, lineWidth, l)
		}
	}
	if rows != maxGroups {
		t.Errorf("%d rows, want %d with the rest folded:\n%s", rows, maxGroups, got)
	}
}

func TestStatusLines_unreachableServer(t *testing.T) {
	st := sync.Status{Plan: sync.Plan{Store: sync.StoreConfig, Pulled: true}, RemoteError: errors.New("dial tcp: refused")}
	got := plainLines(statusLines(st, style{}))
	if got != "▸ config · nothing to commit · server not reachable: dial tcp: refused" {
		t.Fatalf("got %q", got)
	}
}

func TestStyle_colorsOnlyWhenAsked(t *testing.T) {
	theme := utils.TableTheme()
	colored := plainLines(statusLines(manyConversations(), style{color: true}))
	if !strings.Contains(colored, theme.Primary+"▸") || !strings.Contains(colored, theme.Secondary) {
		t.Fatalf("colour output lacks the theme:\n%q", colored)
	}
	if plain := plainLines(statusLines(manyConversations(), style{})); strings.Contains(plain, "\x1b[") {
		t.Fatalf("plain output has escapes:\n%q", plain)
	}
}

func TestBlockedLines(t *testing.T) {
	url := "https://www.clai.chat/account/billing/"
	cases := []struct {
		err  *sync.BillingBlockedError
		want string
	}{
		{&sync.BillingBlockedError{Reason: sync.CardRequired, URL: url}, "▸ conversations · not committed · needs a card on file, $1 per started GB a month\n  → " + url},
		{&sync.BillingBlockedError{Reason: sync.PaymentOverdue, URL: url}, "▸ conversations · not committed · a payment is 7 days overdue\n  → " + url},
		{&sync.BillingBlockedError{Reason: sync.BillingNotOpen, URL: url}, "▸ conversations · not committed · conversation sync is not open yet; config sync works now"},
	}
	for _, c := range cases {
		if got := plainLines(blockedLines(sync.StoreConversations, c.err, style{})); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.err.Reason, got, c.want)
		}
	}
}

func TestStatusLines_previewsOtherTools(t *testing.T) {
	var pending []sync.PendingImport
	for i := range 1200 {
		change := sync.ImportNew
		if i%100 == 0 {
			change = sync.ImportGrown
		}
		pending = append(pending, sync.PendingImport{Source: "claude-code", ID: fmt.Sprint(i), Change: change, Messages: 25, RawBytes: 300_000})
	}
	pending = append(pending, sync.PendingImport{Source: "codex", ID: "x", Change: sync.ImportNew, Messages: 4, RawBytes: 20_000})
	st := sync.Status{
		Plan:    sync.Plan{Store: sync.StoreConversations, Pulled: true, Synced: 12},
		Remote:  &sync.RemoteStatus{},
		Imports: &sync.ImportPreview{Conversations: pending},
	}
	got := plainLines(statusLines(st, style{}))
	for _, want := range []string{
		"▸ conversations · up to date · 12 synced",
		"▸ from other tools · 1.2k conversations to export · 30.0k messages · 360.0 MB in their own files",
		"    tool        conversations  new grown messages     size",
		"  ■ claude-code          1.2k 1.2k    12    30.0k 360.0 MB",
		"  ■ codex                   1    1     0        4  20.0 kB",
		"  → clai cloud commit -imports",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q:\n%s", want, got)
		}
	}

	st.Imports = &sync.ImportPreview{Conversations: []sync.PendingImport{}}
	if got := plainLines(statusLines(st, style{})); !strings.Contains(got, "▸ from other tools · nothing new to export") || strings.Contains(got, "commit -imports") {
		t.Fatalf("an empty preview:\n%s", got)
	}
}
