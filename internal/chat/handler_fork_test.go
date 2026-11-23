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

	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/jsonl"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/stdio"
	"github.com/baalimago/clai/internal/testenv/procenv"
	"github.com/baalimago/clai/pkg/diagnostic"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

const forkPromptRefusal = "chat fork: a prompt is required, as in 'clai chat fork <chat> <prompt>'"

type forkRun struct {
	cq      *ChatHandler
	confDir string
	convDir string
	sent    []ContinueRequest
}

// forkFixtureHandler saves the parent "parent" (model m1) and returns a fork handler for args.
func forkFixtureHandler(t *testing.T, args string, parent pub_models.Chat) *forkRun {
	t.Helper()
	cq, confDir := newTestHandler(t)
	cq.out = &bytes.Buffer{}
	chdirToTemp(t)
	cq.subCmd = "fork"
	cq.prompt = args
	fr := &forkRun{cq: cq, confDir: confDir, convDir: conversationsDir(confDir)}
	saveAll(t, fr.convDir, parent)
	cq.sendPrompt = func(_ context.Context, req ContinueRequest) error {
		fr.sent = append(fr.sent, req)
		return nil
	}
	return fr
}

func savedParent() pub_models.Chat {
	p := forkParent()
	p.Profile = ""
	return p
}

func convSnapshot(t *testing.T, convDir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(convDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(convDir, path)
		out[rel] = fileDigest(t, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	return out
}

func forkIDs(t *testing.T, convDir string) []string {
	t.Helper()
	var ids []string
	for name := range convSnapshot(t, convDir) {
		if filepath.Dir(name) == "." && strings.HasSuffix(name, ".json") && name != "parent.json" {
			ids = append(ids, strings.TrimSuffix(name, ".json"))
		}
	}
	return ids
}

func Test_fork_withoutPromptIsRefused(t *testing.T) {
	for _, args := range []string{"parent", "parent ", "parent   ", "", " "} {
		t.Run("args="+args, func(t *testing.T) {
			fr := forkFixtureHandler(t, args, savedParent())
			before := convSnapshot(t, fr.convDir)
			err := fr.cq.fork(context.Background())
			if err == nil || err.Error() != forkPromptRefusal {
				t.Fatalf("err = %v, want %q", err, forkPromptRefusal)
			}
			if len(fr.sent) != 0 {
				t.Fatal("sender called")
			}
			if !reflect.DeepEqual(before, convSnapshot(t, fr.convDir)) {
				t.Fatal("conversations changed")
			}
			if out := fr.cq.out.(*bytes.Buffer).String(); out != "" {
				t.Fatalf("printed %q", out)
			}
		})
	}
}

func Test_fork_parentNotFound(t *testing.T) {
	fr := forkFixtureHandler(t, "nope hello", savedParent())
	before := convSnapshot(t, fr.convDir)
	err := fr.cq.fork(context.Background())
	want := `chat fork: could not find chat "nope"`
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
	if !reflect.DeepEqual(before, convSnapshot(t, fr.convDir)) || len(fr.sent) != 0 {
		t.Fatal("wrote or sent")
	}
	if out := fr.cq.out.(*bytes.Buffer).String(); out != "" {
		t.Fatalf("list UI printed %q", out)
	}
}

func Test_fork_indexOutOfRangeKeepsTheCause(t *testing.T) {
	fr := forkFixtureHandler(t, "9 hello", savedParent())
	err := fr.cq.fork(context.Background())
	if !errors.Is(err, errIndexOutOfRange) {
		t.Fatalf("err = %v, want errIndexOutOfRange in the chain", err)
	}
}

func Test_fork_writesBeforeSending(t *testing.T) {
	fr := forkFixtureHandler(t, "parent do more", savedParent())
	var seen pub_models.Chat
	var row chatIndexRow
	fr.cq.sendPrompt = func(ctx context.Context, req ContinueRequest) error {
		c, err := fr.cq.getByID(ctx, req.ChatID)
		if err != nil {
			t.Errorf("fork not on disk when sending: %v", err)
			return nil
		}
		seen = c
		row = indexRowOf(t, fr.convDir, req.ChatID)
		fr.sent = append(fr.sent, req)
		return nil
	}
	if err := fr.cq.fork(context.Background()); err != nil {
		t.Fatalf("fork: %v", err)
	}
	if len(seen.Messages) != len(forkParent().Messages) || seen.Messages[len(seen.Messages)-1].Content != "q2" {
		t.Fatalf("fork messages: %+v", seen.Messages)
	}
	if row.ID != seen.ID || seen.SourceID != "parent" || seen.Source != "clai" {
		t.Fatalf("row %+v chat %+v", row, seen)
	}
	if fr.sent[0].Prompt != "do more" {
		t.Fatalf("prompt = %q", fr.sent[0].Prompt)
	}
}

func Test_fork_requestFlagsMatchParent(t *testing.T) {
	for _, tc := range []struct {
		name        string
		flags       []string
		queries     []pub_models.QueryCost
		parentProf  string
		profileFlag string
		wantFlags   []string
		wantProfile string
	}{
		{"no flags", nil, nil, "", "", nil, ""},
		{"-cm is the fork's own", []string{"-cm=m2"}, []pub_models.QueryCost{{Model: "m1"}}, "", "", []string{"-cm=m2"}, ""},
		{"parent model is not carried over", nil, []pub_models.QueryCost{{Model: "m1"}}, "", "", nil, ""},
		{"other flags are kept", []string{"-n"}, []pub_models.QueryCost{{Model: "m1"}}, "", "", []string{"-n"}, ""},
		{"parent profile", nil, nil, "p", "ignored", nil, "p"},
		{"-p stamps a parent with no profile", nil, nil, "", "q", nil, "q"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := savedParent()
			parent.Queries = tc.queries
			parent.Profile = tc.parentProf
			fr := forkFixtureHandler(t, "parent go", parent)
			fr.cq.queryFlags = tc.flags
			fr.cq.profile = tc.profileFlag
			if err := fr.cq.fork(context.Background()); err != nil {
				t.Fatalf("fork: %v", err)
			}
			req := fr.sent[0]
			if len(req.Flags) != len(tc.wantFlags) || (len(tc.wantFlags) > 0 && !reflect.DeepEqual(req.Flags, tc.wantFlags)) {
				t.Fatalf("flags = %v, want %v", req.Flags, tc.wantFlags)
			}
			if req.Profile != tc.wantProfile {
				t.Fatalf("profile = %q, want %q", req.Profile, tc.wantProfile)
			}
			f, err := fr.cq.getByID(context.Background(), req.ChatID)
			if err != nil {
				t.Fatal(err)
			}
			if f.Profile != tc.wantProfile {
				t.Fatalf("fork profile = %q, want %q", f.Profile, tc.wantProfile)
			}
			p, err := fr.cq.getByID(context.Background(), "parent")
			if err != nil {
				t.Fatal(err)
			}
			if p.Profile != tc.parentProf {
				t.Fatalf("parent profile = %q, want %q", p.Profile, tc.parentProf)
			}
		})
	}
}

func Test_fork_parentUntouched(t *testing.T) {
	fr := forkFixtureHandler(t, "parent go", savedParent())
	fr.cq.profile = "stamp"
	fr.cq.sendPrompt = func(context.Context, ContinueRequest) error { return errors.New("boom") }
	path := conversationPathFromDir(fr.convDir, "parent")
	before := fileDigest(t, path)
	rowBefore := indexRowOf(t, fr.convDir, "parent")
	if err := fr.cq.fork(context.Background()); err == nil {
		t.Fatal("want an error")
	}
	if fileDigest(t, path) != before || !reflect.DeepEqual(rowBefore, indexRowOf(t, fr.convDir, "parent")) {
		t.Fatal("parent changed")
	}
	if len(forkIDs(t, fr.convDir)) != 1 {
		t.Fatalf("fork missing: %v", forkIDs(t, fr.convDir))
	}
}

func Test_fork_sendFailureKeepsFork(t *testing.T) {
	fr := forkFixtureHandler(t, "parent go", savedParent())
	fr.cq.sendPrompt = func(_ context.Context, req ContinueRequest) error {
		fr.sent = append(fr.sent, req)
		return errors.New("boom")
	}
	err := fr.cq.fork(context.Background())
	id := fr.sent[0].ChatID
	if err == nil || !strings.Contains(err.Error(), "failed to send prompt to chat "+id+": boom") {
		t.Fatalf("err = %v", err)
	}
	if _, err := fr.cq.getByID(context.Background(), id); err != nil {
		t.Fatalf("fork not loadable: %v", err)
	}
}

func Test_fork_indexUnreadable(t *testing.T) {
	fr := forkFixtureHandler(t, "0 hi", savedParent())
	index := filepath.Join(fr.convDir, chatIndexFileName)
	if err := os.RemoveAll(index); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(index, 0o755); err != nil {
		t.Fatal(err)
	}
	err := fr.cq.fork(context.Background())
	if err == nil || !strings.Contains(err.Error(), "failed to read chat index") {
		t.Fatalf("err = %v", err)
	}
	if len(fr.sent) != 0 || len(forkIDs(t, fr.convDir)) != 0 {
		t.Fatal("wrote or sent")
	}
}

func Test_fork_bindFailureKeepsFork(t *testing.T) {
	fr := forkFixtureHandler(t, "parent hi", savedParent())
	if err := os.RemoveAll(dirscopeRoot(fr.confDir)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dirscopeRoot(fr.confDir), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := fr.cq.fork(context.Background())
	if err == nil || !strings.Contains(err.Error(), "failed to update directory-scoped binding") {
		t.Fatalf("err = %v", err)
	}
	if len(fr.sent) != 0 {
		t.Fatal("sent after a bind failure")
	}
	if len(forkIDs(t, fr.convDir)) != 1 {
		t.Fatalf("fork should stay: %v", forkIDs(t, fr.convDir))
	}
}

func Test_fork_noSenderRefused(t *testing.T) {
	fr := forkFixtureHandler(t, "parent hi", savedParent())
	fr.cq.sendPrompt = nil
	before := convSnapshot(t, fr.convDir)
	err := fr.cq.fork(context.Background())
	if err == nil || err.Error() != "chat fork: this build cannot send a prompt" {
		t.Fatalf("err = %v", err)
	}
	if !reflect.DeepEqual(before, convSnapshot(t, fr.convDir)) {
		t.Fatal("wrote")
	}
}

func Test_fork_noticeLine(t *testing.T) {
	fr := forkFixtureHandler(t, "parent hi", savedParent())
	var stderr bytes.Buffer
	ctx := procenv.Diagnostics(context.Background(), &stderr, diagnostic.LevelInfo)
	if err := fr.cq.fork(ctx); err != nil {
		t.Fatalf("fork: %v", err)
	}
	want := "fork " + fr.sent[0].ChatID + " of parent"
	if !strings.Contains(stderr.String(), want) {
		t.Fatalf("stderr = %q, want %q", stderr.String(), want)
	}
	if out := fr.cq.out.(*bytes.Buffer).String(); out != "" {
		t.Fatalf("stdout = %q", out)
	}
}

func Test_fork_noticeLineJSON(t *testing.T) {
	fr := forkFixtureHandler(t, "parent hi", savedParent())
	var out, errOut bytes.Buffer
	levels := diag.ResolvedLevels{"": diagnostic.LevelInfo}
	for _, ty := range runenv.Types() {
		levels[ty.Subsystem] = diagnostic.LevelInfo
	}
	presenter := diag.NewJSONPresenter(jsonl.NewEncoder(&out), diagnostic.LevelInfo)
	reporter, err := diag.NewReporter(runenv.Types(), levels, map[diagnostic.OutputMode]diagnostic.Deliverer{
		diagnostic.Immediate: presenter,
		diagnostic.Buffered:  presenter,
	})
	if err != nil {
		t.Fatalf("NewReporter: %v", err)
	}
	env := runenv.New(runenv.Config{
		Streams:   stdio.New(stdio.Config{Format: jsonl.FormatJSON, Stdout: &out, Stderr: &errOut}),
		Reporter:  reporter,
		Presenter: presenter,
		Levels:    levels,
		Format:    jsonl.FormatJSON,
	})
	ctx := runenv.With(context.Background(), env)
	if err := fr.cq.fork(ctx); err != nil {
		t.Fatalf("fork: %v", err)
	}
	if err := env.Streams().Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if errOut.String() != "" || strings.Count(out.String(), "\n") != 1 {
		t.Fatalf("want one record on stdout only, got stdout %q stderr %q", out.String(), errOut.String())
	}
	var rec struct{ Type, Name, Subsystem, Level, Message string }
	if err := json.Unmarshal(out.Bytes(), &rec); err != nil {
		t.Fatalf("%q is not a record: %v", out.String(), err)
	}
	want := "fork " + fr.sent[0].ChatID + " of parent"
	if rec.Type != "diagnostic" || rec.Name != runenv.TypeName(runenv.SubsystemChat, runenv.KindNotice) ||
		rec.Subsystem != runenv.SubsystemChat || rec.Message != want {
		t.Fatalf("record = %+v, want a chat notice %q", rec, want)
	}
}

func Test_fork_bindsDirectory(t *testing.T) {
	fr := forkFixtureHandler(t, "parent hi", savedParent())
	if err := fr.cq.fork(context.Background()); err != nil {
		t.Fatalf("fork: %v", err)
	}
	got, err := LoadDirScopeChatID(fr.confDir)
	if err != nil || got != fr.sent[0].ChatID {
		t.Fatalf("bound %q, %v; want the fork %q", got, err, fr.sent[0].ChatID)
	}
}

func Test_fork_forkerFailure(t *testing.T) {
	fr := forkFixtureHandler(t, "parent hi", savedParent())
	cause := errors.New("no entropy")
	fr.cq.forker = NewForker(newMemPersister(), func() (string, error) { return "", cause }, time.Now)
	err := fr.cq.fork(context.Background())
	var fe *ForkError
	if !errors.As(err, &fe) || !errors.Is(err, cause) {
		t.Fatalf("err = %v", err)
	}
	if len(fr.sent) != 0 {
		t.Fatal("sent")
	}
}

func Test_fork_externalRowIsClonedFirst(t *testing.T) {
	cq := continueIndexFixture(t)
	convDir := cq.convDir
	cq.subCmd = "fork"
	cq.prompt = "1 go on"
	var sent []ContinueRequest
	cq.sendPrompt = func(_ context.Context, req ContinueRequest) error { sent = append(sent, req); return nil }
	if err := cq.fork(context.Background()); err != nil {
		t.Fatalf("fork: %v", err)
	}
	fork, err := cq.getByID(context.Background(), sent[0].ChatID)
	if err != nil {
		t.Fatal(err)
	}
	clone, err := cq.getByID(context.Background(), fork.SourceID)
	if err != nil {
		t.Fatalf("clone %q not saved: %v", fork.SourceID, err)
	}
	if clone.Source != "test-source" || clone.SourceID != "ext-1" || fork.Source != "clai" || clone.ID == fork.ID {
		t.Fatalf("clone %+v fork %+v", clone, fork)
	}
	_ = convDir
}

func TestCommand_forkSub(t *testing.T) {
	c := Command(CommandDeps{ConfigPrep: func(context.Context) (string, error) { return t.TempDir(), nil }})
	sub, ok := c.Subcommands()["fork|f"]
	if !ok {
		t.Fatal("missing fork|f")
	}
	fs := sub.Flagset()
	for _, name := range []string{"cm", "p", "prp", "r", "n"} {
		if fs.Lookup(name) == nil {
			t.Fatalf("fork must register -%s", name)
		}
	}
	for _, text := range []string{c.Describe(), chatUsage, sub.Help()} {
		if !strings.Contains(text, "fork") {
			t.Fatalf("%q does not mention fork", text)
		}
	}
	for _, verb := range []string{"continue|c", "fork|f"} {
		if !strings.Contains(c.Subcommands()[verb].Help(), "cc:<chatID>") {
			t.Fatalf("%s help does not name cc:<chatID>", verb)
		}
	}
}
