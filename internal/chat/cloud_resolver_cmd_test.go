package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/cloud"
	"github.com/baalimago/clai/internal/cloud/cloudtest"
	cloudsync "github.com/baalimago/clai/internal/cloud/sync"
	"github.com/baalimago/clai/internal/testenv/procenv"
	"github.com/baalimago/clai/pkg/diagnostic"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type cloudRun struct {
	*forkRun
	srv    *cloudtest.Server
	stderr bytes.Buffer
	ctx    context.Context
}

type clientFactory func(srv *cloudtest.Server) (*cloud.Client, error)

func tokenClient(token string) clientFactory {
	return func(srv *cloudtest.Server) (*cloud.Client, error) { return cloud.NewClient(srv.URL(), token), nil }
}

func newCloudRun(t *testing.T, verb, args string, factory clientFactory) *cloudRun {
	t.Helper()
	fr := forkFixtureHandler(t, args, savedParent())
	fr.cq.subCmd = verb
	run := &cloudRun{forkRun: fr, srv: cloudtest.New(t)}
	run.ctx = procenv.Diagnostics(context.Background(), &run.stderr, diagnostic.LevelInfo)
	fr.cq.cloudResolver = func(_ context.Context, confDir string) (CloudResolver, error) {
		client, err := factory(run.srv)
		if err != nil {
			return nil, err
		}
		engine := &cloudsync.Engine{Client: client, ConfDir: confDir, Now: func() time.Time { return time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC) }}
		return NewCloudResolver(engine, confDir), nil
	}
	return run
}

func chatBytes(t *testing.T, c pub_models.Chat) []byte {
	t.Helper()
	dir := t.TempDir()
	saveAll(t, dir, c)
	b, err := os.ReadFile(filepath.Join(dir, c.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func cloudChatX(messages ...string) pub_models.Chat {
	c := pub_models.Chat{ID: "X", Created: time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC), Queries: []pub_models.QueryCost{{CreatedAt: time.Date(2026, 3, 1, 10, 1, 0, 0, time.UTC), Model: "cloud-model"}}}
	for i, m := range messages {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		c.Messages = append(c.Messages, pub_models.Message{Role: role, Content: m})
	}
	return c
}

func (r *cloudRun) putCloud(t *testing.T, c pub_models.Chat) []byte {
	t.Helper()
	b := chatBytes(t, c)
	r.srv.Put("conversations", "conversations/"+c.ID+".json", string(b))
	return b
}

func (r *cloudRun) run() error { return r.cq.Query(r.ctx) }

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func Test_continueCloud_adopted(t *testing.T) {
	r := newCloudRun(t, "continue", "cc:X", tokenClient("tok-a"))
	want := r.putCloud(t, cloudChatX("hello", "hi there"))
	if err := r.run(); err != nil {
		t.Fatalf("continue: %v", err)
	}
	if got := readFile(t, filepath.Join(r.convDir, "X.json")); got != string(want) {
		t.Fatalf("X.json = %q, want the cloud bytes", got)
	}
	id, err := LoadDirScopeChatID(r.confDir)
	if err != nil || id != "X" {
		t.Fatalf("dir scope = %q, %v", id, err)
	}
	if n := strings.Count(r.stderr.String(), "cc:X: taken from clai.chat"); n != 1 {
		t.Fatalf("stderr = %q", r.stderr.String())
	}
	if n := strings.Count(r.cq.out.(*bytes.Buffer).String(), "hello"); n > 1 {
		t.Fatalf("preview printed %d times", n)
	}
	if len(r.sent) != 0 {
		t.Fatal("sent without a prompt")
	}
}

func Test_continueCloud_withPrompt(t *testing.T) {
	r := newCloudRun(t, "continue", "cc:X and then this", tokenClient("tok-a"))
	r.putCloud(t, cloudChatX("hello", "hi there"))
	if err := r.run(); err != nil {
		t.Fatalf("continue: %v", err)
	}
	if len(r.sent) != 1 || r.sent[0].ChatID != "X" || r.sent[0].Prompt != "and then this" {
		t.Fatalf("sent = %+v", r.sent)
	}
	for _, f := range r.sent[0].Flags {
		if strings.HasPrefix(f, "-cm") {
			t.Fatalf("flags carry a model: %v", r.sent[0].Flags)
		}
	}
}

func Test_continueCloud_notInCloud(t *testing.T) {
	r := newCloudRun(t, "continue", "cc:X hi", tokenClient("tok-a"))
	before := convSnapshot(t, r.convDir)
	err := r.run()
	want := "clai.chat has no conversation X; 'clai chat continue X' continues the local one"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
	var missing *CloudConversationMissingError
	var refErr *CloudRefError
	if !errors.As(err, &missing) || !errors.As(err, &refErr) {
		t.Fatalf("err chain = %v", err)
	}
	if !reflect.DeepEqual(before, convSnapshot(t, r.convDir)) || len(r.sent) != 0 {
		t.Fatal("wrote or sent")
	}
	if out := r.cq.out.(*bytes.Buffer).String(); out != "" {
		t.Fatalf("list UI printed %q", out)
	}
	if _, err := os.Stat(filepath.Join(r.confDir, "cloud")); err == nil {
		t.Fatal("cloud state written")
	}
}

func Test_continueCloud_downloadFailures(t *testing.T) {
	for _, tc := range []struct {
		name    string
		factory clientFactory
		setup   func(*cloudtest.Server)
		check   func(error) bool
	}{
		{"revoked token", tokenClient("tok-revoked"), nil, func(err error) bool {
			var api *cloud.APIError
			return errors.As(err, &api) && api.Unauthorized()
		}},
		{"offline", tokenClient("tok-a"), func(s *cloudtest.Server) { s.Close() }, func(err error) bool {
			var tr *cloud.TransportError
			return errors.As(err, &tr)
		}},
		{"corrupt blob (checksum)", tokenClient("tok-a"), func(s *cloudtest.Server) {
			for _, b := range s.Blobs {
				b[0] ^= 0xff
			}
		}, func(err error) bool {
			var sum *cloudsync.ChecksumError
			var missing *cloudsync.MissingBlobError
			return errors.As(err, &sum) || errors.As(err, &missing)
		}},
		{"not logged in", func(*cloudtest.Server) (*cloud.Client, error) { return nil, &cloud.NotLoggedInError{} }, nil, func(err error) bool {
			var nl *cloud.NotLoggedInError
			return errors.As(err, &nl)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newCloudRun(t, "continue", "cc:X hi", tc.factory)
			r.putCloud(t, cloudChatX("hello", "hi there"))
			if tc.setup != nil {
				tc.setup(r.srv)
			}
			before := convSnapshot(t, r.convDir)
			err := r.run()
			var refErr *CloudRefError
			if err == nil || !errors.As(err, &refErr) || !tc.check(err) {
				t.Fatalf("err = %v (%T)", err, err)
			}
			if !reflect.DeepEqual(before, convSnapshot(t, r.convDir)) || len(r.sent) != 0 {
				t.Fatal("wrote or sent")
			}
		})
	}
}

func Test_forkCloud_pullsThenForks(t *testing.T) {
	r := newCloudRun(t, "fork", "cc:X do it", tokenClient("tok-a"))
	want := r.putCloud(t, cloudChatX("hello", "hi there"))
	if err := r.run(); err != nil {
		t.Fatalf("fork: %v", err)
	}
	if got := readFile(t, filepath.Join(r.convDir, "X.json")); got != string(want) {
		t.Fatal("X differs from the cloud bytes after the fork")
	}
	if len(r.sent) != 1 || r.sent[0].ChatID == "X" || r.sent[0].Prompt != "do it" {
		t.Fatalf("sent = %+v", r.sent)
	}
	if len(r.sent[0].Flags) != 0 {
		t.Fatalf("flags = %v", r.sent[0].Flags)
	}
	f, err := r.cq.getByID(context.Background(), r.sent[0].ChatID)
	if err != nil || f.SourceID != "X" {
		t.Fatalf("fork = %+v, %v", f, err)
	}
}

func Test_forkCloud_localForkAndFork(t *testing.T) {
	r := newCloudRun(t, "fork", "cc:X do it", tokenClient("tok-a"))
	cloudBytes := r.putCloud(t, cloudChatX("hello", "hi there"))
	saveAll(t, r.convDir, cloudChatX("hello", "hi there", "more", "and more"))
	if err := r.run(); err != nil {
		t.Fatalf("fork: %v", err)
	}
	if got := readFile(t, filepath.Join(r.convDir, "X.json")); got != string(cloudBytes) {
		t.Fatal("X is not the cloud copy")
	}
	ids := forkIDs(t, r.convDir)
	if len(ids) != 3 {
		t.Fatalf("ids = %v", ids)
	}
	fork := r.sent[0].ChatID
	seen := map[string]bool{"X": true, fork: true}
	var local string
	for _, id := range ids {
		if !seen[id] {
			local = id
		}
		seen[id] = true
	}
	if local == "" || local == fork || len(seen) != 3 {
		t.Fatalf("want local copy, X and fork distinct; ids %v fork %s", ids, fork)
	}
	l, err := r.cq.getByID(context.Background(), local)
	if err != nil || len(l.Messages) != 4 {
		t.Fatalf("local copy = %+v, %v", l, err)
	}
	f, err := r.cq.getByID(context.Background(), fork)
	if err != nil || f.SourceID != "X" || len(f.Messages) != 2 {
		t.Fatalf("fork = %+v, %v", f, err)
	}
	if !strings.Contains(r.stderr.String(), "the local copy is now "+local) {
		t.Fatalf("stderr = %q", r.stderr.String())
	}
}

func Test_continueCloud_stateAndRecord(t *testing.T) {
	r := newCloudRun(t, "continue", "cc:X", tokenClient("tok-a"))
	r.putCloud(t, cloudChatX("hello", "hi there"))
	saveAll(t, r.convDir, pub_models.Chat{ID: "R"})
	if err := SavePreviousQuery(context.Background(), r.confDir, "R", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(r.confDir, "cloud", "state", "conversations.json")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := `{"cursor":7,"commit":3,"pulled":true,"entries":{},"hashes":{}}`
	if err := os.WriteFile(statePath, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	record := readFile(t, PreviousQueryPath(r.confDir))
	commits := len(r.srv.Commits["conversations"])
	if err := r.run(); err != nil {
		t.Fatalf("continue: %v", err)
	}
	var st struct {
		Cursor, Commit int64
		Entries        map[string]json.RawMessage
	}
	if err := json.Unmarshal([]byte(readFile(t, statePath)), &st); err != nil {
		t.Fatal(err)
	}
	if st.Cursor != 7 || st.Commit != 3 {
		t.Fatalf("state cursor/commit = %d/%d, want 7/3", st.Cursor, st.Commit)
	}
	if _, ok := st.Entries["conversations/X.json"]; !ok {
		t.Fatalf("entries = %v", st.Entries)
	}
	if got := readFile(t, PreviousQueryPath(r.confDir)); got != record {
		t.Fatalf("record changed: %q", got)
	}
	if len(r.srv.Commits["conversations"]) != commits {
		t.Fatal("a commit was made")
	}
}
