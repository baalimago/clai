package cloudcmd

import (
	"encoding/json"
	"strings"
	"testing"
)

type rawCommit struct {
	Store     string   `json:"store"`
	Status    string   `json:"status"`
	Commit    int64    `json:"commit"`
	Files     int      `json:"files"`
	Conflicts []string `json:"conflicts"`
	Behind    []struct {
		Path string `json:"path"`
	} `json:"behind"`
	Billing *struct {
		Reason string `json:"reason"`
		URL    string `json:"url"`
	} `json:"billing"`
}

func decodeCommits(t *testing.T, out string) map[string]rawCommit {
	t.Helper()
	var got []rawCommit
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout is not only JSON: %v\n%s", err, out)
	}
	byStore := map[string]rawCommit{}
	for _, c := range got {
		byStore[c.Store] = c
	}
	return byStore
}

func TestCommitRaw_billingBlockedIsJSONAndExits4(t *testing.T) {
	h := newHarness(t, "tok-a")
	unlock := h.srv.Lock()
	h.srv.CardOnFile = false
	unlock()
	h.write("profiles/a.json", `{"prompt":"a"}`)
	h.write("conversations/abc.json", `{"messages":[]}`)
	code, out := h.run("commit", "-r")
	if code != ExitDecisionNeeded {
		t.Fatalf("code %d: %s", code, out)
	}
	got := decodeCommits(t, out)
	if c := got["config"]; c.Status != "committed" || c.Commit != 1 || c.Files != 1 {
		t.Fatalf("config = %+v", c)
	}
	c := got["conversations"]
	if c.Status != "billing" || c.Billing == nil || c.Billing.Reason != "card_required" || !strings.Contains(c.Billing.URL, "/account/billing") {
		t.Fatalf("conversations = %+v", c)
	}
}

func TestCommit_billingBlockedExits4AfterCommittingConfig(t *testing.T) {
	h := newHarness(t, "tok-a")
	unlock := h.srv.Lock()
	h.srv.CardOnFile = false
	unlock()
	h.write("profiles/a.json", `{"prompt":"a"}`)
	h.write("conversations/abc.json", `{"messages":[]}`)
	code, out := h.run("commit")
	if code != ExitDecisionNeeded {
		t.Fatalf("code %d: %s", code, out)
	}
	if got, _ := h.srv.Content("config", "profiles/a.json"); got == "" {
		t.Fatal("config was not committed")
	}
}

func TestCommitRaw_behindAndConflictsAreJSON(t *testing.T) {
	h := newHarness(t, "tok-a")
	h.write("profiles/x.json", "1")
	h.run("commit")
	h.srv.Put("config", "profiles/x.json", "theirs")
	h.write("profiles/x.json", "mine")
	code, out := h.run("commit", "-r")
	if code != ExitDecisionNeeded {
		t.Fatalf("code %d: %s", code, out)
	}
	got := decodeCommits(t, out)
	if c := got["config"]; c.Status != "behind" || len(c.Behind) != 1 || c.Behind[0].Path != "profiles/x.json" {
		t.Fatalf("config = %+v", c)
	}
	if c := got["conversations"]; c.Status != "up_to_date" {
		t.Fatalf("conversations = %+v", c)
	}

	h.run("pull")
	code, out = h.run("commit", "-r", "config")
	if code != ExitDecisionNeeded {
		t.Fatalf("code %d: %s", code, out)
	}
	if c := decodeCommits(t, out)["config"]; c.Status != "conflicts" || len(c.Conflicts) != 1 {
		t.Fatalf("config = %+v\n%s", c, out)
	}
}

func TestCommit_defaultMessageNamesTheHostWithoutADevice(t *testing.T) {
	h := newHarness(t, "")
	t.Setenv("CLAI_CLOUD_TOKEN", "tok-a")
	t.Setenv("CLAI_CLOUD_URL", h.srv.URL())
	h.write("profiles/a.json", "a")
	if code, out := h.run("commit"); code != 0 {
		t.Fatalf("code %d: %s", code, out)
	}
	_, out := h.run("log", "config")
	mustContain(t, out, `"from laptop"`)
}

func TestCommit_billingNotOpenIsNoDecision(t *testing.T) {
	h := newHarness(t, "tok-a")
	unlock := h.srv.Lock()
	h.srv.CardOnFile, h.srv.BillingClosed = false, true
	unlock()
	h.write("profiles/a.json", `{"prompt":"a"}`)
	h.write("conversations/abc.json", `{"messages":[]}`)
	code, out := h.run("commit", "-r")
	if code != 0 {
		t.Fatalf("code %d: %s", code, out)
	}
	if c := decodeCommits(t, out)["conversations"]; c.Status != "billing" || c.Billing.Reason != "billing_not_open" {
		t.Fatalf("conversations = %+v", c)
	}
}
