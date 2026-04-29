package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func runOne(t *testing.T, cwd string, args string) (string, int) {
	t.Helper()
	oldArgs := os.Args
	t.Cleanup(func() {
		os.Args = oldArgs
	})

	if chDirErr := os.Chdir(cwd); chDirErr != nil {
		t.Fatalf("Chdir(%q): %v", cwd, chDirErr)
	}

	var status int
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		status = run(strings.Split(args, " "))
	})
	return stdout, status
}

func Test_goldenFile_CHAT_DIRSCOPED(t *testing.T) {
	type chatDirInfo struct {
		Scope         string         `json:"scope"`
		ChatID        string         `json:"chat_id"`
		RepliesByRole map[string]int `json:"replies_by_role"`
		TokensTotal   int            `json:"tokens_total"`
	}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldWd)
	})

	confDir := setupMainTestConfigDir(t)
	projRoot := t.TempDir()
	bar := filepath.Join(projRoot, "bar")
	baz := filepath.Join(bar, "baz")
	if mkdirErr := os.MkdirAll(baz, 0o755); mkdirErr != nil {
		t.Fatalf("MkdirAll(baz): %v", mkdirErr)
	}

	parseChatDir := func(t *testing.T, stdout string) chatDirInfo {
		t.Helper()
		trimmed := strings.TrimSpace(strings.TrimSuffix(stdout, "\a"))
		if trimmed == "" {
			t.Fatalf("expected non-empty stdout")
		}
		if trimmed == "{}" {
			return chatDirInfo{}
		}
		var got chatDirInfo
		if err := json.Unmarshal([]byte(trimmed), &got); err != nil {
			t.Fatalf("Unmarshal(chat dir json): %v\nstdout=%q", err, stdout)
		}
		return got
	}

	_, status := runOne(t, bar, "-r -cm test chat dir")
	if status != 0 {
		t.Fatalf("expected zero status for 'chat dir' when empty, got %v", status)
	}

	out, status := runOne(t, bar, "-r -cm test q hello")
	testboil.FailTestIfDiff(t, status, 0)
	testboil.FailTestIfDiff(t, out, "hello\n\a")

	out, status = runOne(t, bar, "-r -cm test chat dir")
	testboil.FailTestIfDiff(t, status, 0)
	barInfo := parseChatDir(t, out)
	if barInfo.Scope != "dir" {
		t.Fatalf("expected scope=dir, got %q", barInfo.Scope)
	}
	if barInfo.ChatID == "" {
		t.Fatalf("expected non-empty chat_id for dir scope, got %q", barInfo.ChatID)
	}
	if barInfo.RepliesByRole == nil {
		t.Fatalf("expected replies_by_role to be present")
	}
	if barInfo.RepliesByRole["user"] < 1 {
		t.Fatalf("expected at least 1 user message, got %v", barInfo.RepliesByRole)
	}
	if barInfo.TokensTotal < 0 {
		t.Fatalf("expected non-negative tokens_total, got %d", barInfo.TokensTotal)
	}

	out, status = runOne(t, bar, "-r re")
	testboil.FailTestIfDiff(t, status, 0)
	testboil.AssertStringContains(t, out, "hello")

	_, status = runOne(t, baz, "-r dre")
	if status == 0 {
		t.Fatalf("expected non-zero status for 'dre' without binding")
	}

	out, status = runOne(t, baz, "-r -cm test q baz")
	testboil.FailTestIfDiff(t, status, 0)
	testboil.FailTestIfDiff(t, out, "baz\n\a")

	out, status = runOne(t, baz, "-r -cm test chat dir")
	testboil.FailTestIfDiff(t, status, 0)
	bazInfo := parseChatDir(t, out)
	if bazInfo.Scope != "dir" {
		t.Fatalf("expected scope=dir, got %q", bazInfo.Scope)
	}
	if bazInfo.ChatID == "" {
		t.Fatalf("expected non-empty chat_id for dir scope")
	}
	if bazInfo.RepliesByRole == nil {
		t.Fatalf("expected replies_by_role to be present")
	}
	if bazInfo.RepliesByRole["user"] < 1 {
		t.Fatalf("expected at least 1 user message, got %v", bazInfo.RepliesByRole)
	}

	out, status = runOne(t, baz, "-r dre")
	testboil.FailTestIfDiff(t, status, 0)
	testboil.AssertStringContains(t, out, "baz")

	out, status = runOne(t, bar, "-r dre")
	testboil.FailTestIfDiff(t, status, 0)
	testboil.AssertStringContains(t, out, "hello")

	out, status = runOne(t, bar, "-r re")
	testboil.FailTestIfDiff(t, status, 0)
	testboil.AssertStringContains(t, out, "baz")

	out, status = runOne(t, baz, "-r -cm test -re -dre q hello3")
	testboil.FailTestIfDiff(t, status, 0)
	testboil.FailTestIfDiff(t, out, "hello3\n\a")

	convDir := filepath.Join(confDir, "conversations")
	entries, err := os.ReadDir(convDir)
	if err != nil {
		t.Fatalf("ReadDir(conversations): %v", err)
	}

	var convFiles []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".json") && name != "globalScope.json" {
			convFiles = append(convFiles, filepath.Join(convDir, name))
		}
	}
	if len(convFiles) == 0 {
		t.Fatalf("expected at least one conversation json in %q", convDir)
	}

	var bazConvPath string
	for _, p := range convFiles {
		b, readErr := os.ReadFile(p)
		if readErr != nil {
			t.Fatalf("ReadFile(%q): %v", p, readErr)
		}
		s := string(b)
		if strings.Contains(s, "baz") && strings.Contains(s, "hello3") {
			bazConvPath = p
			break
		}
	}
	if bazConvPath == "" {
		t.Fatalf("could not find baz conversation file in %q", convDir)
	}

	var bazChat models.Chat
	bazBytes, err := os.ReadFile(bazConvPath)
	if err != nil {
		t.Fatalf("ReadFile(baz conversation): %v", err)
	}
	if unmarshalErr := json.Unmarshal(bazBytes, &bazChat); unmarshalErr != nil {
		t.Fatalf("Unmarshal(baz conversation): %v", unmarshalErr)
	}
	var gotSysMsgs []string
	for _, m := range bazChat.Messages {
		if m.Role == "system" {
			gotSysMsgs = append(gotSysMsgs, m.Content)
		}
	}

	if !slices.Contains(gotSysMsgs, "baz") || !slices.Contains(gotSysMsgs, "hello3") {
		t.Fatalf("expected systemMessages: '%v' to contain: '%v'", gotSysMsgs, []string{"baz", "hello3"})
	}

	bazAbs, err := filepath.Abs(baz)
	if err != nil {
		t.Fatalf("Abs(baz): %v", err)
	}
	sum := sha256.Sum256([]byte(filepath.Clean(bazAbs)))
	hash := hex.EncodeToString(sum[:])

	bindingPath := filepath.Join(confDir, "conversations", "dirs", hash+".json")
	bindingBytes, err := os.ReadFile(bindingPath)
	if err != nil {
		t.Fatalf("ReadFile(bindingPath %q): %v", bindingPath, err)
	}

	type binding struct {
		ChatID string `json:"chat_id"`
	}
	var b binding
	if err := json.Unmarshal(bindingBytes, &b); err != nil {
		t.Fatalf("Unmarshal(binding): %v", err)
	}
	if b.ChatID == "" {
		t.Fatalf("expected non-empty chat_id in binding file %q", bindingPath)
	}
	wantChatFile := filepath.Join(confDir, "conversations", b.ChatID+".json")
	if filepath.Clean(wantChatFile) != filepath.Clean(bazConvPath) {
		t.Fatalf("binding chat_id points to %q, but baz conversation is %q", wantChatFile, bazConvPath)
	}
}

func Test_goldenFile_CHAT_CONTINUE_obfuscated_preview(t *testing.T) {
	// Desired CLI contract:
	// - `clai ... chat continue <index>` prints messages as:
	//   [#nr r: "<role>" l: 00042]: <msg-preview>
	// - It should NOT enter interactive mode.
	// - It binds the current working directory to the continued chat, so it can be
	//   replied to using -dre.
	// - It prints a notice about the new replyable mechanism.

	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}
	t.Setenv("HOME", confDir)
	t.Setenv("CLAI_CONFIG_DIR", confDir)

	convDir := filepath.Join(confDir, "conversations")
	conv := models.Chat{
		Created: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		ID:      "hello-chat",
		Messages: []models.Message{
			{Role: "system", Content: strings.Repeat("a", 200)},
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "world"},
		},
	}
	if err := chat.Save(convDir, conv); err != nil {
		t.Fatalf("Save: %v", err)
	}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldWd)
	})

	out, status := runOne(t, confDir, "-r -cm test c c 0")
	if status != 0 {
		t.Fatalf("expected status code 0, got %v. stdout=%q", status, out)
	}

	// shortened pretty output for the long system message
	testboil.AssertStringContains(t, out, "...")
	testboil.AssertStringContains(t, out, "and 100 more runes")

	// last message is fully printed
	testboil.AssertStringContains(t, out, "world")

	// replyable notice
	testboil.AssertStringContains(t, out, "is now replyable with flag")

	// ensure dirscope binding exists and points to chat
	chatID, err := chat.LoadDirScopeChatID(confDir)
	if err != nil {
		t.Fatalf("LoadDirScopeChatID: %v", err)
	}
	if chatID != conv.ID {
		t.Fatalf("expected dirscope chat id %q got %q", conv.ID, chatID)
	}
}

func Test_e2e_same_prompt_twice_creates_two_separate_chats(t *testing.T) {
	confDir := setupMainTestConfigDir(t)
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldWd)
	})

	prompt := "please keep these as separate chats"
	_, status := runOne(t, confDir, "-r -cm test q "+prompt)
	testboil.FailTestIfDiff(t, status, 0)

	_, status = runOne(t, confDir, "-r -cm test q "+prompt)
	testboil.FailTestIfDiff(t, status, 0)

	entries, err := os.ReadDir(filepath.Join(confDir, "conversations"))
	if err != nil {
		t.Fatalf("ReadDir(conversations): %v", err)
	}

	chatFiles := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") || name == "globalScope.json" {
			continue
		}
		chatFiles = append(chatFiles, name)
	}

	if len(chatFiles) < 2 {
		t.Fatalf("expected at least 2 separate persisted chats for same prompt, got %d: %v", len(chatFiles), chatFiles)
	}
}

func Test_e2e_newly_saved_conversation_has_created_timestamp(t *testing.T) {
	confDir := setupMainTestConfigDir(t)
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldWd)
	})

	_, status := runOne(t, confDir, "-r -cm test q verify created timestamp persists")
	testboil.FailTestIfDiff(t, status, 0)

	entries, err := os.ReadDir(filepath.Join(confDir, "conversations"))
	if err != nil {
		t.Fatalf("ReadDir(conversations): %v", err)
	}

	var conversationPath string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") || name == "globalScope.json" {
			continue
		}
		conversationPath = filepath.Join(confDir, "conversations", name)
		break
	}
	if conversationPath == "" {
		t.Fatalf("expected a saved conversation file in %q", filepath.Join(confDir, "conversations"))
	}

	var saved models.Chat
	b, err := os.ReadFile(conversationPath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", conversationPath, err)
	}
	if err := json.Unmarshal(b, &saved); err != nil {
		t.Fatalf("Unmarshal(%q): %v", conversationPath, err)
	}

	if saved.Created.IsZero() {
		t.Fatalf("expected saved conversation %q to have non-zero created timestamp, got zero value; raw=%s", conversationPath, string(b))
	}
}

func Test_e2e_dre_queries_continue_directory_scoped_conversation(t *testing.T) {
	confDir := setupMainTestConfigDir(t)

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldWd)
	})

	workDir := filepath.Join(t.TempDir(), "a")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", workDir, err)
	}

	type chatDirInfo struct {
		Scope         string         `json:"scope"`
		ChatID        string         `json:"chat_id"`
		RepliesByRole map[string]int `json:"replies_by_role"`
		TokensTotal   int            `json:"tokens_total"`
	}

	parseChatDir := func(t *testing.T, stdout string) chatDirInfo {
		t.Helper()
		trimmed := strings.TrimSpace(strings.TrimSuffix(stdout, "\a"))
		if trimmed == "" {
			t.Fatalf("expected non-empty stdout")
		}
		var got chatDirInfo
		if err := json.Unmarshal([]byte(trimmed), &got); err != nil {
			t.Fatalf("Unmarshal(chat dir json): %v\nstdout=%q", err, stdout)
		}
		return got
	}

	out, status := runOne(t, workDir, "-r -cm test q I like blue")
	testboil.FailTestIfDiff(t, status, 0)
	testboil.FailTestIfDiff(t, out, "I like blue\n\a")

	out, status = runOne(t, workDir, "-r -cm test chat dir")
	testboil.FailTestIfDiff(t, status, 0)
	initialDirInfo := parseChatDir(t, out)
	if initialDirInfo.ChatID == "" {
		t.Fatalf("expected initial directory-scoped chat id to be set")
	}
	initialConversationPath := filepath.Join(confDir, "conversations", initialDirInfo.ChatID+".json")
	initialConversationBytes, err := os.ReadFile(initialConversationPath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", initialConversationPath, err)
	}
	var initialConversation models.Chat
	if err := json.Unmarshal(initialConversationBytes, &initialConversation); err != nil {
		t.Fatalf("Unmarshal(%q): %v", initialConversationPath, err)
	}
	if initialConversation.ID != initialDirInfo.ChatID {
		t.Fatalf("expected initial conversation file %q to decode to chat ID %q, got %q", initialConversationPath, initialDirInfo.ChatID, initialConversation.ID)
	}

	for _, tc := range []struct {
		name string
		args string
	}{
		{name: "first dre reply", args: "-r -cm test -dre q I like cookies"},
		{name: "second dre reply", args: "-r -cm test -dre q I like buns"},
		{name: "third dre reply", args: "-r -cm test -dre q I like tacos"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, status := runOne(t, workDir, tc.args)
			testboil.FailTestIfDiff(t, status, 0)
		})
	}

	out, status = runOne(t, workDir, "-r -cm test chat dir")
	testboil.FailTestIfDiff(t, status, 0)
	finalDirInfo := parseChatDir(t, out)
	if finalDirInfo.ChatID != initialDirInfo.ChatID {
		t.Fatalf("expected directory-scoped chat id to remain %q across -dre replies, got %q", initialDirInfo.ChatID, finalDirInfo.ChatID)
	}
	finalConversationBytes, err := os.ReadFile(initialConversationPath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", initialConversationPath, err)
	}
	var finalConversation models.Chat
	if err := json.Unmarshal(finalConversationBytes, &finalConversation); err != nil {
		t.Fatalf("Unmarshal(%q): %v", initialConversationPath, err)
	}
	if finalConversation.ID != initialDirInfo.ChatID {
		t.Fatalf("expected final conversation file %q to keep chat ID %q, got %q", initialConversationPath, initialDirInfo.ChatID, finalConversation.ID)
	}

	entries, err := os.ReadDir(filepath.Join(confDir, "conversations"))
	if err != nil {
		t.Fatalf("ReadDir(conversations): %v", err)
	}

	var conversationFiles []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".json") || entry.Name() == "globalScope.json" || entry.Name() == "chat_index.cache" {
			continue
		}
		conversationFiles = append(conversationFiles, filepath.Join(confDir, "conversations", entry.Name()))
	}

	if len(conversationFiles) != 1 {
		t.Fatalf("expected exactly 1 persisted conversation after repeated -dre queries, got %d: %v", len(conversationFiles), conversationFiles)
	}

	b, err := os.ReadFile(conversationFiles[0])
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", conversationFiles[0], err)
	}

	var persisted models.Chat
	if err := json.Unmarshal(b, &persisted); err != nil {
		t.Fatalf("Unmarshal(%q): %v", conversationFiles[0], err)
	}

	var userMessages []string
	for _, msg := range persisted.Messages {
		if msg.Role != "user" {
			continue
		}
		userMessages = append(userMessages, msg.Content)
	}

	wantMessages := []string{
		"I like blue",
		"I like cookies",
		"I like buns",
		"I like tacos",
	}
	if !slices.Equal(userMessages, wantMessages) {
		t.Fatalf("expected user messages %v in single persisted conversation, got %v", wantMessages, userMessages)
	}
}

func Test_e2e_dre_setup_keeps_bound_chat_id(t *testing.T) {
	confDir := setupMainTestConfigDir(t)

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldWd)
	})

	workDir := filepath.Join(t.TempDir(), "reply-bound")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", workDir, err)
	}
	if err := os.Chdir(workDir); err != nil {
		t.Fatalf("Chdir(%q): %v", workDir, err)
	}

	_, status := runOne(t, workDir, "-r -cm test q initial prompt")
	testboil.FailTestIfDiff(t, status, 0)

	initialChatID, err := chat.LoadDirScopeChatID(confDir)
	if err != nil {
		t.Fatalf("LoadDirScopeChatID(initial): %v", err)
	}
	if initialChatID == "" {
		t.Fatalf("expected initial dirscope chat id to be non-empty")
	}

	_, status = runOne(t, workDir, "-r -cm test -dre q follow-up prompt")
	testboil.FailTestIfDiff(t, status, 0)

	finalChatID, err := chat.LoadDirScopeChatID(confDir)
	if err != nil {
		t.Fatalf("LoadDirScopeChatID(final): %v", err)
	}
	if finalChatID != initialChatID {
		t.Fatalf("expected -dre query to keep dirscope binding on %q, got %q", initialChatID, finalChatID)
	}
}

func Test_e2e_dre_profile_swap_does_not_create_new_conversation(t *testing.T) {
	confDir := setupMainTestConfigDir(t)
	for _, name := range []string{"first", "second", "third"} {
		profilePath := filepath.Join(confDir, "profiles", name+".json")
		profileContent := `{"name":"` + name + `","model":"test"}`
		if err := os.WriteFile(profilePath, []byte(profileContent), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", profilePath, err)
		}
	}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldWd)
	})

	workDir := filepath.Join(t.TempDir(), "profile-swap")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", workDir, err)
	}

	out, status := runOne(t, workDir, "-r -cm test q seed prompt")
	testboil.FailTestIfDiff(t, status, 0)
	testboil.FailTestIfDiff(t, out, "seed prompt\n\a")

	initialChatID, err := chat.LoadDirScopeChatID(confDir)
	if err != nil {
		t.Fatalf("LoadDirScopeChatID(initial): %v", err)
	}
	if initialChatID == "" {
		t.Fatalf("expected initial dirscope chat id to be non-empty")
	}

	for _, tc := range []struct {
		name string
		args string
	}{
		{name: "swap to second profile", args: "-r -cm test -p second -dre q follow-up one"},
		{name: "swap to third profile", args: "-r -cm test -p third -dre q follow-up two"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, status := runOne(t, workDir, tc.args)
			testboil.FailTestIfDiff(t, status, 0)
			testboil.AssertStringContains(t, out, "\n")

			gotChatID, err := chat.LoadDirScopeChatID(confDir)
			if err != nil {
				t.Fatalf("LoadDirScopeChatID(%s): %v", tc.name, err)
			}
			if gotChatID != initialChatID {
				t.Fatalf("expected dirscope chat id to remain %q after profile swap, got %q", initialChatID, gotChatID)
			}
		})
	}

	entries, err := os.ReadDir(filepath.Join(confDir, "conversations"))
	if err != nil {
		t.Fatalf("ReadDir(conversations): %v", err)
	}

	var conversationFiles []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".json") || entry.Name() == "globalScope.json" || entry.Name() == "chat_index.cache" {
			continue
		}
		conversationFiles = append(conversationFiles, filepath.Join(confDir, "conversations", entry.Name()))
	}

	if len(conversationFiles) != 1 {
		t.Fatalf("expected exactly 1 persisted conversation after -dre profile swaps, got %d: %v", len(conversationFiles), conversationFiles)
	}

	b, err := os.ReadFile(conversationFiles[0])
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", conversationFiles[0], err)
	}

	var persisted models.Chat
	if err := json.Unmarshal(b, &persisted); err != nil {
		t.Fatalf("Unmarshal(%q): %v", conversationFiles[0], err)
	}
	if persisted.ID != initialChatID {
		t.Fatalf("expected persisted conversation id %q, got %q", initialChatID, persisted.ID)
	}
	if persisted.Profile != "third" {
		t.Fatalf("expected persisted conversation profile %q, got %q", "third", persisted.Profile)
	}
	globalScopeBytes, err := os.ReadFile(filepath.Join(confDir, "conversations", "globalScope.json"))
	if err != nil {
		t.Fatalf("ReadFile(globalScope.json): %v", err)
	}
	var globalScope models.Chat
	if err := json.Unmarshal(globalScopeBytes, &globalScope); err != nil {
		t.Fatalf("Unmarshal(globalScope.json): %v", err)
	}
	if globalScope.Profile != "third" {
		t.Fatalf("expected globalScope profile %q, got %q", "third", globalScope.Profile)
	}

	var userMessages []string
	for _, msg := range persisted.Messages {
		if msg.Role != "user" {
			continue
		}
		userMessages = append(userMessages, msg.Content)
	}

	wantMessages := []string{
		"seed prompt",
		"follow-up one",
		"follow-up two",
	}
	if !slices.Equal(userMessages, wantMessages) {
		t.Fatalf("expected user messages %v in single persisted conversation, got %v; globalScopeProfile=%q globalScopeID=%q", wantMessages, userMessages, globalScope.Profile, globalScope.ID)
	}
}
