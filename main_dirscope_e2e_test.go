package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// dirScopeFile mirrors the on-disk binding for assertions (test-local).
type dirScopeFile struct {
	Version int    `json:"version"`
	DirHash string `json:"dir_hash"`
	AbsPath string `json:"abs_path"`
	ChatID  string `json:"chat_id"`
	History []struct {
		ChatID      string    `json:"chat_id"`
		FirstScoped time.Time `json:"first_scoped"`
		LastScoped  time.Time `json:"last_scoped"`
	} `json:"history"`
	Updated time.Time `json:"updated"`
}

func chdirTemp(t *testing.T) string {
	t.Helper()
	repoDir := t.TempDir()
	oldWd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("Chdir(%q): %v", repoDir, err)
	}
	canon, err := filepath.EvalSymlinks(repoDir)
	if err != nil {
		canon = repoDir
	}
	return filepath.Clean(canon)
}

func readOnlyBinding(t *testing.T, confDir string) dirScopeFile {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(confDir, "conversations", "dirs", "*.json"))
	if err != nil {
		t.Fatalf("Glob(dirs): %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected exactly one binding file, got %d: %v", len(matches), matches)
	}
	var ds dirScopeFile
	if err := json.Unmarshal([]byte(readStringFile(t, matches[0])), &ds); err != nil {
		t.Fatalf("Unmarshal(binding): %v", err)
	}
	return ds
}

func conversationFiles(t *testing.T, confDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(confDir, "conversations"))
	if err != nil {
		t.Fatalf("ReadDir(conversations): %v", err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || e.Name() == "globalScope.json" || e.Name() == "chat_index.cache" {
			continue
		}
		out = append(out, filepath.Join(confDir, "conversations", e.Name()))
	}
	return out
}

// Covers E2E expectation 1: recording is unconditional (no -lb) and writes a v2
// binding whose history lists both ids newest-first with abs_path and typed updated.
func Test_e2e_dirscope_recording_is_unconditional(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	confDir := setupMainTestConfigDir(t)
	t.Setenv("CLAI_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	canonWd := chdirTemp(t)

	if status := run(strings.Split("-r -cm mock_test q first query", " ")); status != 0 {
		t.Fatalf("first query status %d", status)
	}
	if status := run(strings.Split("-r -cm mock_test q second query", " ")); status != 0 {
		t.Fatalf("second query status %d", status)
	}

	ds := readOnlyBinding(t, confDir)
	if ds.Version != 2 {
		t.Fatalf("expected version 2 binding, got %d", ds.Version)
	}
	if ds.AbsPath != canonWd {
		t.Fatalf("expected abs_path %q, got %q", canonWd, ds.AbsPath)
	}
	if ds.Updated.IsZero() {
		t.Fatalf("expected typed updated to be set")
	}
	if len(ds.History) != 2 {
		t.Fatalf("expected 2 history entries, got %d: %+v", len(ds.History), ds.History)
	}
	if ds.History[0].ChatID != ds.ChatID {
		t.Fatalf("expected head to equal newest history entry, head=%q history[0]=%q", ds.ChatID, ds.History[0].ChatID)
	}
}

// Covers E2E expectation 2: a reply (-re) does not record.
func Test_e2e_dirscope_reply_does_not_record(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	confDir := setupMainTestConfigDir(t)
	t.Setenv("CLAI_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	chdirTemp(t)

	if status := run(strings.Split("-r -cm mock_test q seed query", " ")); status != 0 {
		t.Fatalf("seed query status %d", status)
	}
	before := readOnlyBinding(t, confDir)
	if len(before.History) != 1 {
		t.Fatalf("expected 1 history entry after seed, got %d", len(before.History))
	}

	if status := run(strings.Split("-r -re -cm mock_test q reply query", " ")); status != 0 {
		t.Fatalf("reply query status %d", status)
	}
	after := readOnlyBinding(t, confDir)
	if len(after.History) != 1 || after.ChatID != before.ChatID {
		t.Fatalf("expected reply to leave history unchanged, before=%+v after=%+v", before, after)
	}
}

// Covers the -dre recording behavior: a directory reply continues the bound
// conversation in place (same id) and DOES upsert it into the directory history
// (bumping it), unlike a plain -re which forks and must not record.
func Test_e2e_dirscope_dre_records_in_place(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	confDir := setupMainTestConfigDir(t)
	t.Setenv("CLAI_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	chdirTemp(t)

	if status := run(strings.Split("-r -cm mock_test q seed conversation here", " ")); status != 0 {
		t.Fatalf("seed query status %d", status)
	}
	before := readOnlyBinding(t, confDir)
	if len(before.History) != 1 {
		t.Fatalf("expected 1 history entry after seed, got %d", len(before.History))
	}
	seedID := before.ChatID
	convsBefore := conversationFiles(t, confDir)

	if status := run(strings.Split("-r -dre -cm mock_test q follow up turn", " ")); status != 0 {
		t.Fatalf("dre query status %d", status)
	}
	after := readOnlyBinding(t, confDir)

	// In place: no new conversation forked, head unchanged.
	if got := conversationFiles(t, confDir); len(got) != len(convsBefore) {
		t.Fatalf("expected -dre to update in place (no fork), conversations %d -> %d", len(convsBefore), len(got))
	}
	if after.ChatID != seedID || len(after.History) != 1 || after.History[0].ChatID != seedID {
		t.Fatalf("expected -dre to keep head %q with a single bumped history entry, got %+v", seedID, after)
	}
	// Recorded: the history entry's LastScoped advanced past the seed.
	if !after.History[0].LastScoped.After(before.History[0].LastScoped) {
		t.Fatalf("expected -dre to bump last_scoped (%v -> %v)", before.History[0].LastScoped, after.History[0].LastScoped)
	}
	// FirstScoped preserved across the in-place reply.
	if !after.History[0].FirstScoped.Equal(before.History[0].FirstScoped) {
		t.Fatalf("expected FirstScoped preserved %v, got %v", before.History[0].FirstScoped, after.History[0].FirstScoped)
	}
}

// Covers E2E expectation 3: a version 1 binding upgrades to version 2 in place at
// the same path on the next query.
func Test_e2e_dirscope_v1_upgrades_in_place(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	confDir := setupMainTestConfigDir(t)
	t.Setenv("CLAI_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	chdirTemp(t)

	// Seed a binding, then downgrade it on disk to a version 1 record.
	if status := run(strings.Split("-r -cm mock_test q seed", " ")); status != 0 {
		t.Fatalf("seed status %d", status)
	}
	matches, _ := filepath.Glob(filepath.Join(confDir, "conversations", "dirs", "*.json"))
	if len(matches) != 1 {
		t.Fatalf("expected one binding, got %v", matches)
	}
	bindingPath := matches[0]
	ds := readOnlyBinding(t, confDir)
	v1 := `{"version":1,"dir_hash":"` + ds.DirHash + `","chat_id":"` + ds.ChatID + `","updated":"2024-01-02T03:04:05Z"}`
	if err := os.WriteFile(bindingPath, []byte(v1), 0o644); err != nil {
		t.Fatalf("downgrade write: %v", err)
	}

	if status := run(strings.Split("-r -cm mock_test q next", " ")); status != 0 {
		t.Fatalf("next status %d", status)
	}

	matchesAfter, _ := filepath.Glob(filepath.Join(confDir, "conversations", "dirs", "*.json"))
	if len(matchesAfter) != 1 || matchesAfter[0] != bindingPath {
		t.Fatalf("expected same single binding path, got %v", matchesAfter)
	}
	upgraded := readOnlyBinding(t, confDir)
	if upgraded.Version != 2 {
		t.Fatalf("expected upgrade to v2, got %d", upgraded.Version)
	}
	if len(upgraded.History) == 0 {
		t.Fatalf("expected history seeded on upgrade")
	}
}

// Covers E2E expectation 4: origin stamping on first persist, mirrored to index,
// and unchanged by a reply.
func Test_e2e_dirscope_origin_stamping(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	confDir := setupMainTestConfigDir(t)
	t.Setenv("CLAI_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	canonWd := chdirTemp(t)

	if status := run(strings.Split("-r -cm mock_test q origin query", " ")); status != 0 {
		t.Fatalf("query status %d", status)
	}
	convs := conversationFiles(t, confDir)
	if len(convs) != 1 {
		t.Fatalf("expected one conversation, got %v", convs)
	}
	convBefore := readStringFile(t, convs[0])
	if !strings.Contains(convBefore, `"origin_dir": "`+canonWd+`"`) {
		t.Fatalf("expected origin_dir %q stamped, got %s", canonWd, convBefore)
	}
	index := readStringFile(t, filepath.Join(confDir, "conversations", "chat_index.cache"))
	if !strings.Contains(index, `"origin_dir":"`+canonWd+`"`) {
		t.Fatalf("expected origin_dir mirrored to index, got %s", index)
	}

	// A reply must not change the original conversation's origin_dir.
	if status := run(strings.Split("-r -re -cm mock_test q reply", " ")); status != 0 {
		t.Fatalf("reply status %d", status)
	}
	convAfter := readStringFile(t, convs[0])
	if !strings.Contains(convAfter, `"origin_dir": "`+canonWd+`"`) {
		t.Fatalf("expected origin_dir preserved after reply, got %s", convAfter)
	}
}

// Covers E2E expectation 5: lookback is off by default — no descriptor, no tools.
func Test_e2e_dirscope_lookback_off_by_default(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	confDir := setupMainTestConfigDir(t)
	t.Setenv("CLAI_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	chdirTemp(t)

	if status := run(strings.Split("-r -cm mock_test q seed history", " ")); status != 0 {
		t.Fatalf("seed status %d", status)
	}
	// History is recorded, but without -lb the tool is not registered, so the mock
	// can not call it and no descriptor is injected.
	stdout, stderr := captureStdoutStderr(t, func() {
		_ = run(strings.Split("-r -cm mock_test q please tool_search_conversations", " "))
	})
	combined := stdout + stderr
	if strings.Contains(combined, "match(es) in") {
		t.Fatalf("expected no search output without -lb, got %q", combined)
	}
	for _, conv := range conversationFiles(t, confDir) {
		if strings.Contains(readStringFile(t, conv), "recent_conversations") {
			t.Fatalf("expected no descriptor block without -lb in %s", conv)
		}
	}
}

// Covers the decoupling fix: with -lb in a directory that has NO recorded
// history, the search tools are still registered and dispatchable (so the agent can
// investigate other paths), while no descriptor block is injected.
func Test_e2e_dirscope_lookback_tools_without_local_history(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	confDir := setupMainTestConfigDir(t)
	t.Setenv("CLAI_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	chdirTemp(t)

	// Fresh directory: no prior query, so the CWD binding has no history.
	t.Setenv("CLAI_MOCK_SEARCH_QUERY", "anything")
	stdout, stderr := captureStdoutStderr(t, func() {
		_ = run(strings.Split("-r -lb -cm mock_test q please tool_search_conversations", " "))
	})
	combined := stdout + stderr
	// Tool is registered and dispatched (header present), even with zero matches.
	if !strings.Contains(combined, "match(es) in") {
		t.Fatalf("expected search tool registered/dispatched without local history, got %q", combined)
	}
	// No descriptor block, since there is no local history.
	for _, conv := range conversationFiles(t, confDir) {
		if strings.Contains(readStringFile(t, conv), "recent_conversations") {
			t.Fatalf("expected no descriptor block without local history in %s", conv)
		}
	}
}

// Covers E2E expectation 6/7/8: with -lb and seeded history the descriptor is
// injected, and search_conversations / inspect_conversation / read_message work.
func Test_e2e_dirscope_lookback_on_tools_work(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	confDir := setupMainTestConfigDir(t)
	t.Setenv("CLAI_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	chdirTemp(t)

	// Seed a conversation with a distinctive keyword.
	if status := run(strings.Split("-r -cm mock_test q oauthrefreshtoken seed", " ")); status != 0 {
		t.Fatalf("seed status %d", status)
	}
	seedConvs := conversationFiles(t, confDir)
	if len(seedConvs) != 1 {
		t.Fatalf("expected one seed conversation, got %v", seedConvs)
	}
	seedID := strings.TrimSuffix(filepath.Base(seedConvs[0]), ".json")

	// search_conversations finds the seeded conversation and the descriptor is injected.
	t.Setenv("CLAI_MOCK_SEARCH_QUERY", "oauthrefreshtoken")
	stdout, stderr := captureStdoutStderr(t, func() {
		_ = run(strings.Split("-r -lb -cm mock_test q please tool_search_conversations", " "))
	})
	combined := stdout + stderr
	if !strings.Contains(combined, "match(es) in") {
		t.Fatalf("expected search output with -lb, got %q", combined)
	}
	if !strings.Contains(combined, "id="+seedID) {
		t.Fatalf("expected the seeded id %q in search output, got %q", seedID, combined)
	}
	// Descriptor injected into the new conversation's system prompt.
	var foundDescriptor bool
	for _, conv := range conversationFiles(t, confDir) {
		if strings.Contains(readStringFile(t, conv), "recent_conversations") {
			foundDescriptor = true
		}
	}
	if !foundDescriptor {
		t.Fatalf("expected descriptor block injected with -lb")
	}

	// inspect_conversation lists the seeded conversation's messages.
	t.Setenv("CLAI_MOCK_INSPECT_CHAT_ID", seedID)
	stdout, stderr = captureStdoutStderr(t, func() {
		_ = run(strings.Split("-r -lb -cm mock_test q please tool_inspect_conversation", " "))
	})
	combined = stdout + stderr
	if !strings.Contains(combined, "Conversation "+seedID+":") || !strings.Contains(combined, "index=0 role=system") {
		t.Fatalf("expected inspect listing for %q, got %q", seedID, combined)
	}

	// read_message returns a single message's role-tagged content.
	t.Setenv("CLAI_MOCK_READ_CHAT_ID", seedID)
	t.Setenv("CLAI_MOCK_READ_INDEX", "1")
	stdout, stderr = captureStdoutStderr(t, func() {
		_ = run(strings.Split("-r -lb -cm mock_test q please tool_read_message", " "))
	})
	combined = stdout + stderr
	if !strings.Contains(combined, "[user]") || !strings.Contains(combined, "oauthrefreshtoken") {
		t.Fatalf("expected read_message user content, got %q", combined)
	}

	// An out-of-range index returns an error tool result and the run continues.
	t.Setenv("CLAI_MOCK_READ_INDEX", "999")
	stdout, stderr = captureStdoutStderr(t, func() {
		if status := run(strings.Split("-r -lb -cm mock_test q please tool_read_message", " ")); status != 0 {
			t.Fatalf("expected run to continue on out-of-range, status nonzero")
		}
	})
	combined = stdout + stderr
	if !strings.Contains(combined, "ERROR:") || !strings.Contains(combined, "out of range") {
		t.Fatalf("expected out-of-range error tool result, got %q", combined)
	}
}
