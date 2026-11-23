package e2e

import (
	"strings"
	"testing"

	"github.com/baalimago/clai/e2e/e2etest"
)

func summaryChatRow(t *testing.T, stderr string) string {
	t.Helper()
	lines := strings.Split(stderr, "\n")
	for i, l := range lines {
		if !strings.HasPrefix(l, "▸ query done") {
			continue
		}
		row, ok := strings.CutPrefix(lines[i+1], "  chat    ")
		if !ok {
			t.Fatalf("first detail row = %q, want the chat row", lines[i+1])
		}
		return row
	}
	t.Fatalf("stderr has no query summary: %q", stderr)
	return ""
}

func Test_e2e_querySummary_namesChat(t *testing.T) {
	env := setupForkE2E(t, "gpt-a", "gpt-a")

	stdout, stderr := env.mustRun(t, "-r -qs q hello")

	id := summaryChatRow(t, stderr)
	if strings.Contains(stdout, "chat    ") {
		t.Fatalf("stdout carries the summary row: %q", stdout)
	}
	if got := readChatFile(t, env.confDir, id); got.ID != id {
		t.Fatalf("conversations/%s.json holds chat %q", id, got.ID)
	}
}

func Test_e2e_querySummary_forkFromPrintedID(t *testing.T) {
	env := setupForkE2E(t, "gpt-a", "gpt-a")
	_, primed := env.mustRun(t, "-r -qs q first question")
	id := summaryChatRow(t, primed)

	_, stderr := env.mustRun(t, "-r chat fork "+id+" task")

	forkID, parentID := e2etest.ForkNotice(t, stderr)
	if parentID != id || forkID == id {
		t.Fatalf("notice names fork %s of %s, want a fork of %s", forkID, parentID, id)
	}
}
