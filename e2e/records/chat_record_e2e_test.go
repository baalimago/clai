package records

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/baalimago/clai/e2e/e2etest"
)

// chatIDs is the id of each chat record, in order.
func chatIDs(records []jsonRecord) []string {
	var ids []string
	for _, r := range ofType(records, "chat") {
		ids = append(ids, r.str("id"))
	}
	return ids
}

func TestContinueProfilePathReplacesLocalStamp(t *testing.T) {
	confDir := e2etest.SetupMainTestConfigDir(t)
	profile := filepath.Join(confDir, "profiles", "local.json")
	if err := os.WriteFile(profile, []byte(`{"name":"local","model":"mock_test","prompt":"local system"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	first := runOnStreams([]string{"-json", "-p", "local", "q", "seed"}, streamOpts{})
	ids := chatIDs(parseRecords(t, "stdout", first.stdout))
	if first.status != 0 || len(ids) != 1 {
		t.Fatalf("seed: %+v", first)
	}
	override := filepath.Join(confDir, "override.json")
	if err := os.WriteFile(override, []byte(`{"name":"override","model":"mock_test","prompt":"override system"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"-prp", "-profile-path"} {
		next := runOnStreams([]string{"-json", "chat", "continue", "-n", flag, override, ids[0], "next"}, streamOpts{})
		if next.status != 0 {
			t.Fatalf("%s override: status %d stderr %s", flag, next.status, next.stderr)
		}
		if got := chatIDs(parseRecords(t, "stdout", next.stdout)); len(got) != 1 || got[0] != ids[0] {
			t.Fatalf("continued chats %v, want %v", got, ids)
		}
		stored := e2etest.ReadStringFile(t, filepath.Join(confDir, "conversations", ids[0]+".json"))
		if !strings.Contains(stored, `"profile": `+strconv.Quote(override)) {
			t.Fatalf("explicit profile did not replace the stamp: %s", stored)
		}
	}
	next := runOnStreams([]string{"-json", "chat", "continue", "-n", ids[0], "use saved path"}, streamOpts{})
	if next.status != 0 {
		t.Fatalf("continue with saved profile path: %+v", next)
	}
}

func TestContinueProfileWithDifferentDisplayName(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprint(legacy), func(t *testing.T) {
			confDir := e2etest.SetupMainTestConfigDir(t)
			profile := filepath.Join(confDir, "profiles", "me.json")
			if err := os.WriteFile(profile, []byte(`{"name":"Myself","model":"mock_test","prompt":"profile system"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			first := runOnStreams([]string{"-json", "-p", "me", "q", "seed"}, streamOpts{})
			ids := chatIDs(parseRecords(t, "stdout", first.stdout))
			if first.status != 0 || len(ids) != 1 {
				t.Fatalf("seed: %+v", first)
			}
			file := filepath.Join(confDir, "conversations", ids[0]+".json")
			stored := e2etest.ReadStringFile(t, file)
			if legacy {
				var record map[string]json.RawMessage
				if err := json.Unmarshal([]byte(stored), &record); err != nil {
					t.Fatal(err)
				}
				record["profile"] = json.RawMessage(`"Myself"`)
				body, err := json.Marshal(record)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, body, 0o600); err != nil {
					t.Fatal(err)
				}
			} else if !strings.Contains(stored, `"profile": "me"`) {
				t.Fatalf("unstable profile selector: %s", stored)
			}
			next := runOnStreams([]string{"-json", "chat", "continue", "-n", ids[0], "next"}, streamOpts{})
			if next.status != 0 {
				t.Fatalf("continue: %+v", next)
			}
			if got := chatIDs(parseRecords(t, "stdout", next.stdout)); len(got) != 1 || got[0] != ids[0] {
				t.Fatalf("continued chats %v, want %v", got, ids)
			}
			if stored := e2etest.ReadStringFile(t, file); !strings.Contains(stored, `"profile": "me"`) {
				t.Fatalf("continued profile selector: %s", stored)
			}
		})
	}
}

func TestContinueReplyOnlyOmitsPreview(t *testing.T) {
	for _, flag := range []string{"-reply-only", "-ro", "-reply-only=false"} {
		t.Run(flag, func(t *testing.T) {
			e2etest.SetupMainTestConfigDir(t)
			first := runOnStreams(strings.Fields("-json -cm mock_test q PRIVATE-SEED"), streamOpts{})
			ids := chatIDs(parseRecords(t, "stdout", first.stdout))
			if first.status != 0 || len(ids) != 1 {
				t.Fatalf("seed: %+v", first)
			}
			next := runOnStreams([]string{"chat", "continue", "-r", "-n", "-cm", "mock_test", flag, ids[0], "next"}, streamOpts{})
			if next.status != 0 {
				t.Fatalf("continue: %+v", next)
			}
			if flag == "-reply-only=false" {
				if !strings.Contains(next.stdout, "PRIVATE-SEED") {
					t.Fatalf("preview missing when reply-only is off: %q", next.stdout)
				}
			} else if next.stdout != "next\n" || strings.Contains(next.stderr, "PRIVATE-SEED") {
				t.Fatalf("reply-only must omit the preview: %+v", next)
			}
		})
	}
}

func TestContinueReplyOnlyWithoutPromptOmitsPreview(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		for _, selection := range []string{"id", "directory"} {
			t.Run(format+"/"+selection, func(t *testing.T) {
				confDir := e2etest.SetupMainTestConfigDir(t)
				first := runOnStreams(strings.Fields("-json -cm mock_test q PRIVATE-SEED"), streamOpts{})
				ids := chatIDs(parseRecords(t, "stdout", first.stdout))
				if first.status != 0 || len(ids) != 1 {
					t.Fatalf("seed: %+v", first)
				}
				path := filepath.Join(confDir, "conversations", ids[0]+".json")
				before := e2etest.ReadStringFile(t, path)
				args := []string{"chat", "continue", "-reply-only"}
				if selection == "id" {
					args = append(args, ids[0])
				}
				if format == "json" {
					args = append([]string{"-json"}, args...)
				}
				next := runOnStreams(args, streamOpts{})
				if next.status != 0 || next.stdout != "" || strings.Contains(next.stderr, "PRIVATE-SEED") {
					t.Fatalf("reply-only without a prompt must omit the preview: %+v", next)
				}
				if got := e2etest.ReadStringFile(t, path); got != before {
					t.Fatalf("selection without a prompt changed the conversation:\n%s", got)
				}
			})
		}
	}
}

// A query names the conversation it saved in one chat record, on the
// transcript's destination: stderr under -reply-only, stdout otherwise.
// `chat continue <id> <prompt>` under -json continues it and names it again,
// with records only: the chat it continues is not printed.
func TestJSONChatRecordNamesTheSavedConversation(t *testing.T) {
	confDir := e2etest.SetupMainTestConfigDir(t)
	conversation := func(id string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(confDir, "conversations", id+".json"))
		if err != nil {
			t.Fatalf("read conversation %q: %v", id, err)
		}
		return string(b)
	}

	first := runOnStreams(strings.Fields("-json -cm mock_test -reply-only q first meeting"), streamOpts{})
	stdout, stderr := parseRecords(t, "stdout", first.stdout), parseRecords(t, "stderr", first.stderr)
	if first.status != 0 || len(stdout) != 1 || stdout[0].typ() != "reply" {
		t.Fatalf("status %d stdout %v; want one reply record", first.status, stdout)
	}
	ids := chatIDs(stderr)
	if len(ids) != 1 || ids[0] == "" {
		t.Fatalf("stderr %v; want one chat record", stderr)
	}
	id := ids[0]
	if !strings.Contains(conversation(id), `"content": "first meeting"`) {
		t.Fatalf("conversation %s does not hold the query:\n%s", id, conversation(id))
	}

	args := append(strings.Fields("-json chat continue -reply-only -n -cm mock_test "+id), "the answer")
	second := runOnStreams(args, streamOpts{})
	stdout, stderr = parseRecords(t, "stdout", second.stdout), parseRecords(t, "stderr", second.stderr)
	if got := chatIDs(stderr); second.status != 0 || len(got) != 1 || got[0] != id {
		t.Fatalf("status %d chat records %v; want %q again: stdout %v stderr %v", second.status, got, id, stdout, stderr)
	}
	if len(stdout) != 1 || stdout[0].typ() != "reply" || len(ofType(stderr, "output")) != 0 {
		t.Fatalf("stdout %v stderr %v; want the reply alone on stdout and no printed chat", stdout, stderr)
	}
	if !strings.Contains(conversation(id), `"content": "the answer"`) {
		t.Fatalf("conversation %s was not continued:\n%s", id, conversation(id))
	}

	plain := runOnStreams(strings.Fields("-json -cm mock_test q plain"), streamOpts{})
	if got := chatIDs(parseRecords(t, "stdout", plain.stdout)); plain.status != 0 || len(got) != 1 || got[0] == id {
		t.Fatalf("status %d stdout chat records %v; want one new conversation on stdout", plain.status, got)
	}
}
