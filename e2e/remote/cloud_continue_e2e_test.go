package remote

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/e2e/e2etest"
	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/cloud/cloudtest"
	"github.com/baalimago/clai/internal/cost"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

const (
	cloudChatID = "cloud-chat-1"
	cloudPNG    = "png-bytes"
)

var (
	localCopyNotice = regexp.MustCompile(`the local copy is now (\S+)`)
	trashNotice     = regexp.MustCompile(`kept in (cloud/trash/\S+)\)`)
)

type cloudEnv struct {
	confDir string
	srv     *cloudtest.Server
	prov    *e2etest.CachingProvider
}

func setupCloudE2E(t *testing.T) cloudEnv {
	t.Helper()
	confDir := e2etest.SetupMainTestConfigDir(t)
	prov := e2etest.StartCachingProvider(t, "the reply")
	prov.WriteModel(t, confDir, "gpt-a")
	e2etest.WriteTextConfig(t, confDir, "gpt-a")
	srv := cloudtest.New(t)
	t.Setenv("CLAI_CLOUD_URL", srv.URL())
	t.Setenv("CLAI_CLOUD_TOKEN", "tok-a")
	return cloudEnv{confDir: confDir, srv: srv, prov: prov}
}

func (e cloudEnv) run(t *testing.T, args string) (stdout, stderr string, status int) {
	t.Helper()
	return harness.RunOneStreams(t, e.confDir, args)
}

func (e cloudEnv) mustRun(t *testing.T, args string) (stdout, stderr string) {
	t.Helper()
	stdout, stderr, status := e.run(t, args)
	if status != 0 {
		t.Fatalf("clai %s: status %d stdout %q stderr %q", args, status, stdout, stderr)
	}
	return stdout, stderr
}

type cloudQuery struct {
	At     time.Time
	Tokens int
}

// cloudChatJSON is a conversation as clai.chat holds it, its third message carrying an image.
func cloudChatJSON(t *testing.T, id string, texts []string, queries ...cloudQuery) string {
	t.Helper()
	msgs := make([]map[string]any, 0, len(texts))
	for i, s := range texts {
		role, content := "user", any(s)
		if i%2 == 1 {
			role = "assistant"
		}
		if i == 2 {
			content = []map[string]any{{"type": "text", "text": s}, {"type": "image", "asset": imageRef()}}
		}
		msgs = append(msgs, map[string]any{"role": role, "content": content})
	}
	qs := make([]map[string]any, 0, len(queries))
	for _, q := range queries {
		qs = append(qs, map[string]any{
			"created_at": q.At.Format(time.RFC3339Nano), "model": "gpt-a", "cost_usd": 0.01,
			"usage": map[string]any{"prompt_tokens": q.Tokens - 10, "completion_tokens": 10, "total_tokens": q.Tokens},
		})
	}
	groupKey := chat.ComputeGroupKey(pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: texts[0]}}})
	doc := map[string]any{"id": id, "created": "2026-01-01T00:00:00Z", "group_key": groupKey, "messages": msgs, "queries": qs}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func imageRef() string { return cloudtest.Hash([]byte(cloudPNG)) + ".png" }

func (e cloudEnv) putCloudChat(t *testing.T, id string, texts []string, queries ...cloudQuery) string {
	t.Helper()
	body := cloudChatJSON(t, id, texts, queries...)
	e.srv.Put("conversations", "conversations/"+id+".json", body)
	e.srv.Put("conversations", "conversations/assets/"+imageRef(), cloudPNG)
	e.srv.Put("conversations", "conversations/reasoning/"+id+"/c1.json", `[{"id":"r1"}]`)
	return body
}

func (e cloudEnv) path(rel string) string { return filepath.Join(e.confDir, filepath.FromSlash(rel)) }

func (e cloudEnv) read(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(e.path(rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

func (e cloudEnv) chat(t *testing.T, id string) pub_models.Chat {
	t.Helper()
	var c pub_models.Chat
	if err := json.Unmarshal([]byte(e.read(t, "conversations/"+id+".json")), &c); err != nil {
		t.Fatalf("decode %s: %v", id, err)
	}
	return c
}

func (e cloudEnv) fetchedBlobs() int {
	defer e.srv.Lock()()
	return len(e.srv.FetchedHashes)
}

func (e cloudEnv) assertNothingToPull(t *testing.T, id string) {
	t.Helper()
	before := e.read(t, "conversations/"+id+".json")
	fetched := e.fetchedBlobs()
	stdout, stderr, status := e.run(t, "cloud pull conversations")
	if status != 0 {
		t.Fatalf("cloud pull conversations: status %d (4 means a decision is needed)\nstdout %s\nstderr %s", status, stdout, stderr)
	}
	if e.read(t, "conversations/"+id+".json") != before {
		t.Fatalf("the pull changed %s", id)
	}
	if got := e.fetchedBlobs(); got != fetched {
		t.Fatalf("the pull fetched %d blobs", got-fetched)
	}
	if _, err := os.Stat(e.path("cloud/theirs")); err == nil {
		t.Fatal("the pull left a cloud/theirs directory")
	}
	if !strings.Contains(e.read(t, "cloud/state/conversations.json"), "conversations/"+id+".json") {
		t.Fatalf("cloud/state/conversations.json records no base for %s", id)
	}
}

// walkOutcomes takes a cloud conversation through Adopted, Replaced and LocalForked, running after each.
func walkOutcomes(t *testing.T, env cloudEnv, after func(outcome string)) {
	t.Helper()
	t1 := time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC)
	cloud1 := env.putCloudChat(t, cloudChatID, []string{"cloud question", "cloud answer", "see this"}, cloudQuery{t1, 30})

	_, stderr := env.mustRun(t, "-r chat continue cc:"+cloudChatID)
	if !strings.Contains(stderr, "cc:"+cloudChatID+": taken from clai.chat") {
		t.Fatalf("adopted: stderr %q", stderr)
	}
	if env.read(t, "conversations/"+cloudChatID+".json") != cloud1 {
		t.Fatal("X.json is not the server's bytes")
	}
	if env.read(t, "conversations/reasoning/"+cloudChatID+"/c1.json") != `[{"id":"r1"}]` || env.read(t, "conversations/assets/"+imageRef()) != cloudPNG {
		t.Fatal("the sidecar or the image is missing")
	}
	after("adopted")

	cloud2 := env.putCloudChat(t, cloudChatID, []string{"cloud question", "cloud answer", "see this", "cloud second answer"}, cloudQuery{t1, 30}, cloudQuery{t1.Add(time.Second), 50})
	_, stderr = env.mustRun(t, "-r chat continue cc:"+cloudChatID)
	m := trashNotice.FindStringSubmatch(stderr)
	if m == nil || !strings.HasPrefix(m[1], "cloud/trash/conversations/"+cloudChatID+".json.") || !strings.Contains(stderr, "replaced a shorter local copy") {
		t.Fatalf("replaced: stderr %q", stderr)
	}
	if env.read(t, m[1]) != cloud1 {
		t.Fatal("the trash file is not the old local copy")
	}
	if env.read(t, "conversations/"+cloudChatID+".json") != cloud2 {
		t.Fatal("X.json is not the new server copy")
	}
	after("replaced")

	env.mustRun(t, "-r chat continue "+cloudChatID+" local turn")
	local := env.chat(t, cloudChatID)
	cloud3 := env.putCloudChat(t, cloudChatID, []string{"cloud question", "cloud answer", "see this", "another cloud answer"}, cloudQuery{t1, 30}, cloudQuery{t1.Add(time.Second), 50})
	_, stderr = env.mustRun(t, "-r chat continue cc:"+cloudChatID)
	lm := localCopyNotice.FindStringSubmatch(stderr)
	if lm == nil || !strings.Contains(stderr, "cc:"+cloudChatID+": taken from clai.chat; the local copy is now ") {
		t.Fatalf("local forked: stderr %q", stderr)
	}
	fork := env.chat(t, lm[1])
	if len(fork.Messages) != len(local.Messages) || fork.Source != "clai" || fork.SourceID != cloudChatID {
		t.Fatalf("L = %d messages, source %q source_id %q; want the %d local messages, a fork of X", len(fork.Messages), fork.Source, fork.SourceID, len(local.Messages))
	}
	for i, msg := range local.Messages {
		if !reflect.DeepEqual(fork.Messages[i], msg) {
			t.Fatalf("L message %d = %+v, want %+v", i, fork.Messages[i], msg)
		}
	}
	if env.read(t, "conversations/"+cloudChatID+".json") != cloud3 {
		t.Fatal("X.json is not the cloud copy")
	}
	list, _ := env.mustRun(t, "-r -n chat list q")
	if !strings.Contains(list, "[group:2]") {
		t.Fatalf("the list shows no group of X and its fork:\n%s", list)
	}
	after("localForked")
}

func Test_e2e_cloud_continue_threeOutcomes(t *testing.T) {
	env := setupCloudE2E(t)

	walkOutcomes(t, env, func(string) {})

	if n := env.srv.Requests["/api/v1/commit"]; n != 0 {
		t.Fatalf("clai uploaded %d commits", n)
	}
}

func Test_e2e_cloud_continue_then_pull_has_nothing_to_do(t *testing.T) {
	env := setupCloudE2E(t)

	walkOutcomes(t, env, func(string) { env.assertNothingToPull(t, cloudChatID) })
}

func Test_e2e_cloud_continue_withPromptSendsToCloudCopy(t *testing.T) {
	env := setupCloudE2E(t)
	env.putCloudChat(t, cloudChatID, []string{"cloud question", "cloud answer", "see this"})

	env.mustRun(t, "-r chat continue cc:"+cloudChatID+" and then this")

	reqs := env.prov.Requests()
	if len(reqs) != 1 {
		t.Fatalf("provider saw %d requests, want 1", len(reqs))
	}
	body := reqs[0].Body
	for _, want := range []string{"cloud question", "cloud answer", "and then this"} {
		if !strings.Contains(body, want) {
			t.Fatalf("request lacks %q: %s", want, body)
		}
	}
}

func Test_e2e_cloud_fork(t *testing.T) {
	env := setupCloudE2E(t)
	cloud1 := env.putCloudChat(t, cloudChatID, []string{"cloud question", "cloud answer", "see this"}, cloudQuery{time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC), 30})

	_, stderr := env.mustRun(t, "-r chat fork cc:"+cloudChatID+" do it")

	adopt := strings.Index(stderr, "cc:"+cloudChatID+": taken from clai.chat")
	forkID, parentID := e2etest.ForkNotice(t, stderr)
	if adopt < 0 || adopt > strings.Index(stderr, "fork "+forkID+" of "+parentID) || parentID != cloudChatID {
		t.Fatalf("stderr %q, want the adoption notice then fork <F> of %s", stderr, cloudChatID)
	}
	if env.read(t, "conversations/"+cloudChatID+".json") != cloud1 {
		t.Fatal("X is not the cloud copy")
	}
	if fork := env.chat(t, forkID); fork.SourceID != cloudChatID {
		t.Fatalf("fork source_id = %q", fork.SourceID)
	}
	reqs := env.prov.Requests()
	if len(reqs) != 1 || !strings.Contains(reqs[0].Body, "cloud answer") || !strings.Contains(reqs[0].Body, "do it") {
		t.Fatalf("requests = %+v, want one carrying the cloud messages", reqs)
	}
}

func Test_e2e_cloud_refusals(t *testing.T) {
	cases := []struct {
		name  string
		args  string
		setup func(t *testing.T, env cloudEnv)
		want  string
	}{
		{"not in the cloud", "-r chat continue cc:missing", nil, "clai.chat has no conversation missing; 'clai chat continue missing' continues the local one"},
		{"offline", "-r chat continue cc:" + cloudChatID, func(_ *testing.T, env cloudEnv) { env.srv.Close() }, "connection refused"},
		{"not logged in", "-r chat continue cc:" + cloudChatID, func(t *testing.T, _ cloudEnv) { t.Setenv("CLAI_CLOUD_TOKEN", "") }, "not logged in to clai.chat: run 'clai cloud login'"},
		{"fork without a prompt", "-r chat fork cc:" + cloudChatID, nil, "chat fork: a prompt is required, as in 'clai chat fork <chat> <prompt>'"},
		{"delete", "-r chat delete cc:" + cloudChatID, nil, "chat delete: cc: references are for continue and fork; delete the local copy with 'clai chat delete " + cloudChatID + "'"},
		{"no ID", "-r chat continue cc:", nil, `chat continue: "cc:" needs a conversation ID, as in cc:<chatID>`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := setupCloudE2E(t)
			env.putCloudChat(t, cloudChatID, []string{"cloud question", "cloud answer", "see this"})
			if c.setup != nil {
				c.setup(t, env)
			}
			env.run(t, "-r chat delete no-such-chat")
			before := e2etest.ConfigTree(t, env.confDir)

			stdout, stderr, status := env.run(t, c.args)

			if status != 1 || !strings.Contains(stderr, c.want) {
				t.Fatalf("status %d stdout %q stderr %q, want exit 1 with %q", status, stdout, stderr, c.want)
			}
			if after := e2etest.ConfigTree(t, env.confDir); !maps.Equal(before, after) {
				t.Fatalf("the config dir changed: %d files before, %d after", len(before), len(after))
			}
			if len(env.prov.Requests()) != 0 {
				t.Fatal("a refused command sent a request")
			}
		})
	}
}

func Test_e2e_cloud_continue_localForkTotals(t *testing.T) {
	env := setupCloudE2E(t)
	t0 := time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC)
	env.putCloudChat(t, cloudChatID, []string{"cloud question", "cloud answer", "see this"}, cloudQuery{t0, 30})
	env.mustRun(t, "-r chat continue cc:"+cloudChatID)
	env.mustRun(t, "-r chat continue "+cloudChatID+" local turn")
	local := env.chat(t, cloudChatID)
	if len(local.Queries) != 2 {
		t.Fatalf("local X has %d queries, want the cloud one and its own", len(local.Queries))
	}
	q := local.Queries[1]
	env.putCloudChat(t, cloudChatID, []string{"cloud question", "cloud answer", "see this", "cloud second answer"}, cloudQuery{t0, 30}, cloudQuery{t0.Add(time.Second), 70})

	_, stderr := env.mustRun(t, "-r chat continue cc:"+cloudChatID)

	lm := localCopyNotice.FindStringSubmatch(stderr)
	if lm == nil {
		t.Fatalf("no local fork notice: %q", stderr)
	}
	fork := env.chat(t, lm[1])
	if !fork.Created.Equal(q.CreatedAt) {
		t.Fatalf("L.created = %v, want its first query the cloud copy lacks, %v", fork.Created, q.CreatedAt)
	}
	rows := e2etest.IndexRows(t, env.confDir)
	if rows[lm[1]]["own_total_tokens"] != float64(q.Usage.TotalTokens) {
		t.Fatalf("L own_total_tokens = %v, want %d", rows[lm[1]]["own_total_tokens"], q.Usage.TotalTokens)
	}
	if rows[cloudChatID]["own_total_tokens"] != float64(100) || rows[lm[1]]["total_tokens"] != float64(30+q.Usage.TotalTokens) {
		t.Fatalf("rows X %v, L %v", rows[cloudChatID], rows[lm[1]])
	}
	groupCost := rows[cloudChatID]["own_total_cost_usd"].(float64) + rows[lm[1]]["own_total_cost_usd"].(float64)
	list, _ := env.mustRun(t, "-r -n chat list q")
	if !strings.Contains(list, "[group:2]") || !strings.Contains(list, cost.FormatUSD(groupCost)) {
		t.Fatalf("X and L are not one group costing %s:\n%s", cost.FormatUSD(groupCost), list)
	}
	view, _ := env.mustRun(t, "-r -n chat list 0 q")
	if !strings.Contains(view, cost.FormatUSD(rows[lm[1]]["total_cost_usd"].(float64))) {
		t.Fatalf("the group view lacks L's whole history cost %v:\n%s", rows[lm[1]]["total_cost_usd"], view)
	}
}
