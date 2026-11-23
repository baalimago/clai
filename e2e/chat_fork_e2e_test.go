package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/baalimago/clai/e2e/e2etest"
	"github.com/baalimago/clai/internal/chat"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type forkEnv struct {
	confDir string
	prov    *e2etest.CachingProvider
}

func setupForkE2E(t *testing.T, defaultModel string, models ...string) forkEnv {
	t.Helper()
	confDir := e2etest.SetupMainTestConfigDir(t)
	prov := e2etest.StartCachingProvider(t, "the reply")
	for _, m := range models {
		prov.WriteModel(t, confDir, m)
	}
	e2etest.WriteTextConfig(t, confDir, defaultModel)
	return forkEnv{confDir: confDir, prov: prov}
}

func (e forkEnv) run(t *testing.T, args string) (stdout, stderr string, status int) {
	t.Helper()
	stdout, stderr, status = harness.RunOneStreams(t, e.confDir, args)
	return stdout, stderr, status
}

func (e forkEnv) mustRun(t *testing.T, args string) (stdout, stderr string) {
	t.Helper()
	stdout, stderr, status := e.run(t, args)
	if status != 0 {
		t.Fatalf("clai %s: status %d stdout %q stderr %q", args, status, stdout, stderr)
	}
	return stdout, stderr
}

// seed runs one query and returns the chat it saved.
func (e forkEnv) seed(t *testing.T, args string) pub_models.Chat {
	t.Helper()
	e.mustRun(t, args)
	return previousChat(t, e.confDir)
}

func previousChat(t *testing.T, confDir string) pub_models.Chat {
	t.Helper()
	prev, err := chat.LoadPreviousQuery(context.Background(), confDir)
	if err != nil {
		t.Fatalf("LoadPreviousQuery: %v", err)
	}
	return prev.Chat
}

func readChatFile(t *testing.T, confDir, id string) pub_models.Chat {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(confDir, "conversations", id+".json"))
	if err != nil {
		t.Fatalf("read chat %s: %v", id, err)
	}
	var c pub_models.Chat
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatalf("decode chat %s: %v", id, err)
	}
	return c
}

func chatFileBytes(t *testing.T, confDir, id string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(confDir, "conversations", id+".json"))
	if err != nil {
		t.Fatalf("read chat %s: %v", id, err)
	}
	return b
}

func indexRowJSON(t *testing.T, confDir, id string) string {
	t.Helper()
	row, ok := e2etest.IndexRows(t, confDir)[id]
	if !ok {
		t.Fatalf("no index row for %s", id)
	}
	b, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func chatFileIDs(t *testing.T, confDir string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(confDir, "conversations", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(files))
	for _, f := range files {
		ids = append(ids, strings.TrimSuffix(filepath.Base(f), ".json"))
	}
	slices.Sort(ids)
	return ids
}

func decodeRequests(t *testing.T, p *e2etest.CachingProvider, n int) []e2etest.RecordedRequest {
	t.Helper()
	reqs := p.Requests()
	if len(reqs) != n {
		t.Fatalf("provider saw %d requests, want %d", len(reqs), n)
	}
	return reqs
}

func Test_e2e_chat_fork_repeatsParentRequest(t *testing.T) {
	env := setupForkE2E(t, "gpt-a", "gpt-a")
	parent := env.seed(t, "-r q first question")

	_, stderr := env.mustRun(t, "-r -qs chat fork 0 add a second step")

	forkID, parentID := e2etest.ForkNotice(t, stderr)
	if parentID != parent.ID || forkID == parent.ID {
		t.Fatalf("notice names fork %s of %s, want a new fork of %s", forkID, parentID, parent.ID)
	}
	reqs := decodeRequests(t, env.prov, 2)
	first, second := reqs[0], reqs[1]
	if first.Model != second.Model {
		t.Fatalf("models differ: %q then %q", first.Model, second.Model)
	}
	for k, v := range first.Rest {
		if !bytes.Equal(v, second.Rest[k]) {
			t.Fatalf("member %q changed: %s then %s", k, v, second.Rest[k])
		}
	}
	if len(second.Rest) != len(first.Rest) {
		t.Fatalf("members differ: %d then %d", len(first.Rest), len(second.Rest))
	}
	n := len(first.Messages)
	for i := range n {
		if !bytes.Equal(first.Messages[i], second.Messages[i]) {
			t.Fatalf("message %d is not a byte-equal prefix:\n%s\n%s", i, first.Messages[i], second.Messages[i])
		}
	}
	if len(second.Messages) != n+2 {
		t.Fatalf("fork request has %d messages, want %d (prefix, the answer, the new prompt)", len(second.Messages), n+2)
	}
	if !strings.Contains(string(second.Messages[n]), "the reply") || !strings.Contains(string(second.Messages[n+1]), "add a second step") {
		t.Fatalf("tail of the fork request = %s, %s", second.Messages[n], second.Messages[n+1])
	}
	if got, want := env.prov.Usage(1).Cached, env.prov.Usage(0).Prompt; got != want || got == 0 {
		t.Fatalf("cached tokens = %d, want the parent's whole prompt %d", got, want)
	}
	if !strings.Contains(stderr, "(20 cached)") {
		t.Fatalf("usage line lacks (20 cached): %q", stderr)
	}
}

func Test_e2e_chat_fork_withTools(t *testing.T) {
	env := setupForkE2E(t, "gpt-a", "gpt-a")
	profile := map[string]any{"name": "tooled", "model": "gpt-a", "tools": []string{"pwd", "cat"}, "prompt": "You use tools."}
	e2etest.WriteJSONFileAny(t, filepath.Join(env.confDir, "profiles", "tooled.json"), profile)
	env.mustRun(t, "-r -p tooled q first question")
	env.mustRun(t, "-r chat fork 0 next")

	reqs := decodeRequests(t, env.prov, 2)
	tools := reqs[0].Rest["tools"]
	var decoded []json.RawMessage
	if err := json.Unmarshal(tools, &decoded); err != nil || len(decoded) != 2 {
		t.Fatalf("request 1 tools = %s (err %v), want two", tools, err)
	}
	if !bytes.Equal(tools, reqs[1].Rest["tools"]) {
		t.Fatalf("tools differ:\n%s\n%s", tools, reqs[1].Rest["tools"])
	}
}

func Test_e2e_chat_fork_withToolSelectionByName(t *testing.T) {
	for name, sel := range map[string]string{"every tool": "*", "glob": "*_*"} {
		t.Run(name, func(t *testing.T) {
			env := setupForkE2E(t, "gpt-a", "gpt-a")
			env.mustRun(t, "-r -t "+sel+" q first question")
			env.mustRun(t, "-r -t "+sel+" chat fork 0 next")

			reqs := decodeRequests(t, env.prov, 2)
			tools := reqs[0].Rest["tools"]
			var decoded []json.RawMessage
			if err := json.Unmarshal(tools, &decoded); err != nil || len(decoded) < 2 {
				t.Fatalf("request 1 tools = %s (err %v), want several", tools, err)
			}
			if !bytes.Equal(tools, reqs[1].Rest["tools"]) {
				t.Fatalf("tools differ:\n%s\n%s", tools, reqs[1].Rest["tools"])
			}
		})
	}
}

func Test_e2e_chat_fork_parentUntouched(t *testing.T) {
	env := setupForkE2E(t, "gpt-a", "gpt-a")
	env.mustRun(t, "-r q first question")
	parent := env.seed(t, "-r -re q second question")
	parent.Summary = "parent summary"
	if err := os.WriteFile(filepath.Join(env.confDir, "conversations", parent.ID+".json"), mustJSON(t, parent), 0o644); err != nil {
		t.Fatalf("write parent: %v", err)
	}
	before := chatFileBytes(t, env.confDir, parent.ID)
	rowBefore := indexRowJSON(t, env.confDir, parent.ID)
	if len(parent.Queries) != 2 {
		t.Fatalf("parent has %d queries, want 2", len(parent.Queries))
	}

	_, stderr := env.mustRun(t, "-r chat fork 0 branch off")

	if !bytes.Equal(before, chatFileBytes(t, env.confDir, parent.ID)) || rowBefore != indexRowJSON(t, env.confDir, parent.ID) {
		t.Fatal("the fork changed the parent's file or index row")
	}
	forkID, _ := e2etest.ForkNotice(t, stderr)
	fork := readChatFile(t, env.confDir, forkID)
	if fork.ID == parent.ID || fork.Source != "clai" || fork.SourceID != parent.ID {
		t.Fatalf("fork identity = id %q source %q source_id %q", fork.ID, fork.Source, fork.SourceID)
	}
	if len(fork.Messages) < len(parent.Messages) || len(fork.Queries) != len(parent.Queries)+1 {
		t.Fatalf("fork has %d messages and %d queries; parent %d and %d", len(fork.Messages), len(fork.Queries), len(parent.Messages), len(parent.Queries))
	}
	for i, m := range parent.Messages {
		if !jsonEqual(t, m, fork.Messages[i]) {
			t.Fatalf("message %d differs: %+v vs %+v", i, m, fork.Messages[i])
		}
	}
	for i, q := range parent.Queries {
		if !jsonEqual(t, q, fork.Queries[i]) {
			t.Fatalf("query %d differs: %+v vs %+v", i, q, fork.Queries[i])
		}
	}
	if fork.GroupKey == "" || fork.GroupKey != parent.GroupKey || fork.OriginDir != parent.OriginDir || fork.Profile != parent.Profile || fork.Title != parent.Title {
		t.Fatalf("fork fields differ: %+v vs %+v", fork, parent)
	}
	if !fork.Created.After(parent.Created) {
		t.Fatalf("fork Created %v is not after the parent's %v", fork.Created, parent.Created)
	}

	env.prov.FailFrom(3, 500)
	_, failStderr, status := env.run(t, "-r chat fork "+parent.ID+" branch that fails")
	if status != 1 {
		t.Fatalf("failing fork: status %d stderr %q", status, failStderr)
	}
	failedID, _ := e2etest.ForkNotice(t, failStderr)
	inherited := readChatFile(t, env.confDir, failedID)
	if parent.TokenUsage == nil || parent.RecentTokenUsage == nil || parent.Summary == "" {
		t.Fatalf("the parent has no usage or summary to inherit: %+v", parent)
	}
	if !jsonEqual(t, inherited.TokenUsage, parent.TokenUsage) || !jsonEqual(t, inherited.RecentTokenUsage, parent.RecentTokenUsage) || inherited.Summary != parent.Summary {
		t.Fatalf("fork inherited usage %+v %+v summary %q, parent %+v %+v summary %q", inherited.TokenUsage, inherited.RecentTokenUsage, inherited.Summary, parent.TokenUsage, parent.RecentTokenUsage, parent.Summary)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func jsonEqual(t *testing.T, a, b any) bool {
	t.Helper()
	x, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	y, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Equal(x, y)
}

func Test_e2e_chat_fork_model(t *testing.T) {
	env := setupForkE2E(t, "gpt-b", "gpt-a", "gpt-b")
	env.seed(t, "-r -cm gpt-a q first question")

	for _, c := range []struct {
		name, args, wantModel string
	}{
		{"profile's model, not the parent's", "-r chat fork 0 next", "gpt-b"},
		{"-cm repeated", "-r -cm gpt-a chat fork 0 next", "gpt-a"},
		{"-chat-model repeated", "-r -chat-model gpt-a chat fork 0 next", "gpt-a"},
		{"-cm after fork", "-r chat fork -cm gpt-a 0 next", "gpt-a"},
	} {
		t.Run(c.name, func(t *testing.T) {
			before := len(env.prov.Requests())
			env.mustRun(t, c.args)
			reqs := env.prov.Requests()
			if len(reqs) != before+1 || reqs[before].Model != c.wantModel {
				t.Fatalf("requests = %d (was %d), last model %q, want %q", len(reqs), before, reqs[len(reqs)-1].Model, c.wantModel)
			}
		})
	}

	t.Run("parent without a recorded model", func(t *testing.T) {
		bare := e2etest.MakeConv("bare-parent", time.Now(), e2etest.MsgUser("seeded question"), e2etest.MsgAsst("seeded answer"))
		e2etest.SeedConv(t, filepath.Join(env.confDir, "conversations"), bare)
		before := len(env.prov.Requests())
		env.mustRun(t, "-r chat fork bare-parent next")
		reqs := env.prov.Requests()
		if len(reqs) != before+1 || reqs[before].Model != "gpt-b" {
			t.Fatalf("requests = %d (was %d), last model %q, want the default gpt-b", len(reqs), before, reqs[len(reqs)-1].Model)
		}
	})
}

func Test_e2e_chat_fork_unknownModelKeepsFork(t *testing.T) {
	env := setupForkE2E(t, "gpt-a", "gpt-a")
	env.seed(t, "-r q first question")
	idsBefore := chatFileIDs(t, env.confDir)

	_, stderr, status := env.run(t, "-r -cm no-such-model chat fork 0 next")

	if status != 1 || !strings.Contains(stderr, "no-such-model") {
		t.Fatalf("status %d stderr %q, want exit 1 naming the model", status, stderr)
	}
	if got := chatFileIDs(t, env.confDir); len(got) != len(idsBefore)+1 {
		t.Fatalf("chat files %v, want the fork beside %v", got, idsBefore)
	}
	if len(env.prov.Requests()) != 1 {
		t.Fatalf("the provider saw %d requests, want only the seed", len(env.prov.Requests()))
	}
}

func Test_e2e_chat_fork_groupTotals(t *testing.T) {
	env := setupForkE2E(t, "gpt-a", "gpt-a")
	parent := env.seed(t, "-r q first question")
	_, stderrA := env.mustRun(t, "-r chat fork 0 task a")
	forkA, _ := e2etest.ForkNotice(t, stderrA)
	env.mustRun(t, "-r chat fork "+parent.ID+" task b")
	forkB := previousChat(t, env.confDir).ID

	const tP, tA = 30, 50
	rows := e2etest.IndexRows(t, env.confDir)
	wantOwn := map[string]float64{parent.ID: tP, forkA: tA, forkB: tA}
	wantTotal := map[string]float64{parent.ID: tP, forkA: tP + tA, forkB: tP + tA}
	for id, own := range wantOwn {
		if rows[id]["own_total_tokens"] != own || rows[id]["total_tokens"] != wantTotal[id] {
			t.Fatalf("row %s: own %v total %v, want %v and %v", id, rows[id]["own_total_tokens"], rows[id]["total_tokens"], own, wantTotal[id])
		}
	}

	top, status := harness.RunOne(t, env.confDir, "-n -r c l q")
	if status != 0 || !strings.Contains(top, "[group:3]") || !strings.Contains(top, "$0.135") {
		t.Fatalf("group row, status %d:\n%s", status, top)
	}
	inGroup, status := harness.RunOne(t, env.confDir, "-n -r c l 0 q")
	for _, want := range []string{"$0.09 ", "$0.085", "$0.04 "} {
		if status != 0 || !strings.Contains(inGroup, want) {
			t.Fatalf("group view lacks %q, status %d:\n%s", want, status, inGroup)
		}
	}
}

func Test_e2e_chat_fork_concurrent(t *testing.T) {
	env := setupForkE2E(t, "gpt-a", "gpt-a")
	parent := env.seed(t, "-r q first question")
	parentBytes := chatFileBytes(t, env.confDir, parent.ID)
	bin := filepath.Join(e2etest.CLIBinDir(t), "clai")

	const forks = 4
	env.prov.HoldUntil(forks)
	outs := make([][]byte, forks)
	errs := make([]error, forks)
	var wg sync.WaitGroup
	for i := range forks {
		wg.Go(func() {
			cmd := exec.Command(bin, "-r", "chat", "fork", parent.ID, "task "+strconv.Itoa(i))
			outs[i], errs[i] = cmd.CombinedOutput()
		})
	}
	wg.Wait()
	for i := range forks {
		if errs[i] != nil {
			t.Fatalf("fork process %d: %v\n%s", i, errs[i], outs[i])
		}
	}
	if got := env.prov.MaxInFlight(); got < 2 {
		t.Fatalf("at most %d requests were in flight at once, want the fork processes to overlap", got)
	}

	if !bytes.Equal(parentBytes, chatFileBytes(t, env.confDir, parent.ID)) {
		t.Fatal("a fork changed the parent")
	}
	ids := slices.DeleteFunc(chatFileIDs(t, env.confDir), func(id string) bool { return id == parent.ID })
	if len(ids) != forks {
		t.Fatalf("fork files = %v, want %d", ids, forks)
	}
	rows := e2etest.IndexRows(t, env.confDir)
	if len(rows) != forks+1 {
		t.Fatalf("index holds %d rows, want %d", len(rows), forks+1)
	}
	prompts := map[string]bool{}
	for _, id := range ids {
		fork := readChatFile(t, env.confDir, id)
		if _, ok := rows[id]; !ok {
			t.Fatalf("no index row for fork %s", id)
		}
		if len(fork.Messages) != len(parent.Messages)+2 || fork.SourceID != parent.ID {
			t.Fatalf("fork %s holds %d messages (parent %d), source_id %q", id, len(fork.Messages), len(parent.Messages), fork.SourceID)
		}
		last := fork.Messages[len(fork.Messages)-1]
		if last.Role != "assistant" || last.Content != "the reply" {
			t.Fatalf("fork %s ends with %+v, want the answer", id, last)
		}
		prompts[fork.Messages[len(fork.Messages)-2].Content] = true
	}
	if len(prompts) != forks {
		t.Fatalf("forks carry prompts %v, want %d distinct", prompts, forks)
	}
	if !slices.Contains(ids, previousChat(t, env.confDir).ID) {
		t.Fatal("the previous-query record names none of the forks")
	}
}

func Test_e2e_chat_fork_providerError(t *testing.T) {
	env := setupForkE2E(t, "gpt-a", "gpt-a")
	parent := env.seed(t, "-r q first question")
	parentBytes := chatFileBytes(t, env.confDir, parent.ID)
	rowBefore := indexRowJSON(t, env.confDir, parent.ID)
	env.prov.FailFrom(1, 500)

	_, stderr, status := env.run(t, "-r chat fork 0 will fail")

	if status != 1 {
		t.Fatalf("status %d stderr %q, want 1", status, stderr)
	}
	forkID, _ := e2etest.ForkNotice(t, stderr)
	if fork := readChatFile(t, env.confDir, forkID); fork.SourceID != parent.ID {
		t.Fatalf("fork on disk = %+v", fork)
	}
	if _, ok := e2etest.IndexRows(t, env.confDir)[forkID]; !ok {
		t.Fatal("the fork has no index row")
	}
	if !bytes.Equal(parentBytes, chatFileBytes(t, env.confDir, parent.ID)) || rowBefore != indexRowJSON(t, env.confDir, parent.ID) {
		t.Fatal("the parent changed")
	}
}

func Test_e2e_chat_fork_thenReContinuesFork(t *testing.T) {
	env := setupForkE2E(t, "gpt-a", "gpt-a")
	parent := env.seed(t, "-r q first question")
	parentBytes := chatFileBytes(t, env.confDir, parent.ID)
	_, stderr := env.mustRun(t, "-r chat fork 0 fork turn")
	forkID, _ := e2etest.ForkNotice(t, stderr)

	env.mustRun(t, "-r -re q follow up")

	reqs := env.prov.Requests()
	last := reqs[len(reqs)-1]
	joined := strings.Join(rawStrings(last.Messages), "\n")
	if !strings.Contains(joined, "fork turn") || !strings.Contains(joined, "follow up") {
		t.Fatalf("the reply request does not hold the fork's turns: %s", joined)
	}
	fork := readChatFile(t, env.confDir, forkID)
	if got := fork.Messages[len(fork.Messages)-2].Content; got != "follow up" {
		t.Fatalf("the fork's last prompt = %v, want the follow up", got)
	}
	if !bytes.Equal(parentBytes, chatFileBytes(t, env.confDir, parent.ID)) {
		t.Fatal("the reply went to the parent")
	}
}

func rawStrings(msgs []json.RawMessage) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = string(m)
	}
	return out
}

func Test_e2e_chat_fork_withoutPrompt(t *testing.T) {
	env := setupForkE2E(t, "gpt-a", "gpt-a")
	env.seed(t, "-r q first question")
	idsBefore := chatFileIDs(t, env.confDir)

	for _, args := range []string{"-r chat fork 0", "-r chat fork"} {
		_, stderr, status := env.run(t, args)
		if status != 1 || !strings.Contains(stderr, "chat fork: a prompt is required, as in 'clai chat fork <chat> <prompt>'") {
			t.Fatalf("%q: status %d stderr %q, want the P6 refusal", args, status, stderr)
		}
	}
	if got := chatFileIDs(t, env.confDir); !slices.Equal(got, idsBefore) {
		t.Fatalf("chat files %v, want %v", got, idsBefore)
	}
	if n := len(env.prov.Requests()); n != 1 {
		t.Fatalf("the provider saw %d requests, want only the seed", n)
	}
}

func Test_e2e_chat_fork_externalRow(t *testing.T) {
	env := setupForkE2E(t, "gpt-a", "gpt-a")
	home := t.TempDir()
	t.Setenv("HOME", home)
	projDir := filepath.Join(home, ".claude", "projects", "p1")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	jsonl := strings.Join([]string{
		`{"type":"user","timestamp":"2026-01-01T00:00:00Z","sessionId":"s-fork","cwd":"/work","message":{"content":"foreign question"}}`,
		`{"type":"assistant","timestamp":"2026-01-01T00:00:01Z","sessionId":"s-fork","cwd":"/work","message":{"content":"foreign answer"}}`,
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(projDir, "sess.jsonl"), []byte(jsonl), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}

	native := env.seed(t, "-r q a native chat")

	_, stderr := env.mustRun(t, "-r chat fork 1 and now this")

	forkID, cloneID := e2etest.ForkNotice(t, stderr)
	ids := chatFileIDs(t, env.confDir)
	if !slices.Contains(ids, forkID) || !slices.Contains(ids, cloneID) || !slices.Contains(ids, native.ID) || len(ids) != 3 {
		t.Fatalf("chat files %v, want the clone %s and the fork %s", ids, cloneID, forkID)
	}
	if readChatFile(t, env.confDir, forkID).SourceID != cloneID {
		t.Fatal("the fork is not of the native clone")
	}
	reqs := decodeRequests(t, env.prov, 2)
	joined := strings.Join(rawStrings(reqs[1].Messages), "\n")
	if !strings.Contains(joined, "foreign question") || !strings.Contains(joined, "foreign answer") || !strings.Contains(joined, "and now this") {
		t.Fatalf("request lacks the foreign messages: %s", joined)
	}
}

func Test_e2e_chat_list_continuesLine(t *testing.T) {
	env := setupForkE2E(t, "gpt-a", "gpt-a")
	env.seed(t, "-r q first question")
	env.mustRun(t, "-r chat fork 0 branch off")
	before := e2etest.ConfigTree(t, env.confDir)

	stdout, _ := env.mustRun(t, "-r -n chat list q")
	first, rest, _ := strings.Cut(stdout, "\n")
	if first != "-re continues: 0 [group] · first question" || !strings.Contains(rest, "Index") {
		t.Fatalf("first line %q, then:\n%s", first, rest)
	}
	if strings.Index(rest, "Index") > strings.Index(rest, "[group:2]") {
		t.Fatalf("the header does not precede the rows:\n%s", rest)
	}

	jsonOut, _ := env.mustRun(t, "-json -n chat list q")
	if strings.Contains(jsonOut, "-re continues:") {
		t.Fatalf("-json output has the line:\n%s", jsonOut)
	}
	if !maps.Equal(before, e2etest.ConfigTree(t, env.confDir)) {
		t.Fatal("chat list wrote to the config tree")
	}
}

func Test_e2e_help_listsForkAndCloudRef(t *testing.T) {
	env := setupForkE2E(t, "gpt-a", "gpt-a")
	for _, c := range []struct {
		args string
		want []string
	}{
		{"-h", []string{"chat|c"}},
		{"chat -h", []string{"fork|f"}},
		{"chat help", []string{"f|fork <chatID> <prompt>", "cc:<chatID>"}},
		{"chat fork -h", []string{"chat fork:", "clai c f cc:<chatID> go another way", "The prompt is required"}},
	} {
		stdout, stderr, status := env.run(t, c.args)
		all := stdout + stderr
		for _, want := range c.want {
			if status != 0 || !strings.Contains(all, want) {
				t.Fatalf("clai %s: status %d, output lacks %q:\n%s", c.args, status, want, all)
			}
		}
	}
	top, _ := env.mustRun(t, "-h")
	line := ""
	for l := range strings.SplitSeq(top, "\n") {
		if strings.Contains(l, "chat|c") {
			line = l
		}
	}
	if !strings.Contains(line, "fork") {
		t.Fatalf("the top-level chat line does not list fork: %q", line)
	}
}
