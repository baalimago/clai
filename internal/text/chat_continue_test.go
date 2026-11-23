package text

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/testenv/procenv"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func Test_ApplyFlagOverrides_continueChat(t *testing.T) {
	tf := tfWith(t, func(tf *internal.TextFlags) { tf.ContinueChat = "c1" })
	conf := Default
	ApplyFlagOverrides(&conf, tf)
	if conf.ReplyChatID != "c1" || !conf.ReplyMode || conf.DirReplyMode {
		t.Fatalf("ReplyChatID %q ReplyMode %t DirReplyMode %t; want c1, a reply, not a directory reply", conf.ReplyChatID, conf.ReplyMode, conf.DirReplyMode)
	}
}

// A continued chat's InitialChat is that chat: the previous query is not read
// into it.
func TestConfigurations_SetupInitialChat_continuedChatSkipsPreviousQuery(t *testing.T) {
	confDir := t.TempDir()
	stale := pub_models.Chat{ID: "stale-chat", Created: time.Now(), Messages: []pub_models.Message{
		{Role: "system", Content: "sys"}, {Role: "user", Content: "stale query"}, {Role: "assistant", Content: "stale response"},
	}}
	seedPreviousQuery(t, confDir, stale)
	named := pub_models.Chat{ID: "named", Messages: []pub_models.Message{
		{Role: "system", Content: "sys"}, {Role: "user", Content: "first meeting"}, {Role: "assistant", Content: "who is Speaker A?"},
	}}

	conf := Default
	conf.ConfigDir = confDir
	conf.ReplyMode = true
	conf.ReplyChatID = "named"
	conf.InitialChat = named
	if err := conf.SetupInitialChat(context.Background(), []string{"q", "Ada"}); err != nil {
		t.Fatalf("SetupInitialChat: %v", err)
	}
	got := conf.InitialChat
	if got.ID != "named" || len(got.Messages) != 4 || got.Messages[1].Content != "first meeting" || got.Messages[3].Content != "Ada" {
		t.Fatalf("InitialChat = %q %v; want the named conversation and the answer", got.ID, got.Messages)
	}
}

func TestContinueArgs_givenProfilePathWinsOverTheStamp(t *testing.T) {
	pin := "cchub:a/b#sha256=" + strings.Repeat("c", 64)
	args := continueArgs(chat.ContinueRequest{
		ChatID: "c1", Prompt: "-not a flag", Profile: "cchub:a/b", Raw: true, Flags: []string{"-prp=" + pin, "-reply-only=true"},
	})
	want := []string{"-r", "-prp=" + pin, "-reply-only=true", "--", "-not a flag"}
	if !slices.Equal(args, want) {
		t.Fatalf("args = %q, want %q", args, want)
	}
	c := QueryCommand(QueryCommandDeps{})
	if err := c.Flagset().Parse(args); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := c.Flagset().Lookup("prp").Value.String(); got != pin {
		t.Fatalf("-prp = %q, want the pin %q", got, pin)
	}
	if got := c.Flagset().Args(); !slices.Equal(got, []string{"-not a flag"}) {
		t.Fatalf("prompt args = %q", got)
	}
}

func TestContinueArgs_profilePathReplacesLocalStamp(t *testing.T) {
	for _, name := range []string{"prp", "profile-path"} {
		t.Run(name, func(t *testing.T) {
			args := continueArgs(chat.ContinueRequest{Profile: "local", Prompt: "next", Flags: []string{"-" + name + "=override.json"}})
			c := QueryCommand(QueryCommandDeps{})
			if err := c.Flagset().Parse(args); err != nil {
				t.Fatal(err)
			}
			if got := c.Flagset().Lookup("p").Value.String(); got != "" {
				t.Fatalf("local stamp still selected: %q", got)
			}
			if got := c.Flagset().Lookup("prp").Value.String(); got != "override.json" {
				t.Fatalf("profile path = %q, want override.json", got)
			}
		})
	}
}

// The race chat continue had: it bound the directory to the chat, then the
// run it started read the binding back. A run that ends in the same
// directory in between binds its own chat, and the prompt went there. The
// directory is rebound here at the moment the run sets up.
func TestContinueSender_continuesTheNamedChatWhateverTheDirectoryBinds(t *testing.T) {
	t.Setenv("CLAI_DISABLE_COST_ERR_LOG_GOROUTINE", "1")
	readonly, noCreate, live := utils.ReadonlyConfig, utils.NoCreateConfig, utils.Live
	t.Cleanup(func() { utils.ReadonlyConfig, utils.NoCreateConfig, utils.Live = readonly, noCreate, live })
	confDir := t.TempDir()
	t.Setenv("CLAI_CONFIG_DIR", confDir)
	t.Setenv("HOME", t.TempDir())
	writeMockPriceFile(t, confDir)
	seedEmptyChatIndex(t, confDir)
	t.Chdir(t.TempDir())
	ctx := procenv.Context(context.Background())
	convDir := filepath.Join(confDir, "conversations")
	meeting := func(id, text string) pub_models.Chat {
		return pub_models.Chat{ID: id, Created: time.Now(), Messages: []pub_models.Message{
			{Role: "system", Content: "sys"}, {Role: "user", Content: text}, {Role: "assistant", Content: "who is Speaker A?"},
		}}
	}
	for _, c := range []pub_models.Chat{meeting("first", "first meeting"), meeting("second", "second meeting")} {
		if err := chat.Save(ctx, convDir, c); err != nil {
			t.Fatalf("Save(%s): %v", c.ID, err)
		}
	}
	if err := chat.UpdateDirScopeFromCWD(ctx, confDir, "first"); err != nil {
		t.Fatalf("bind first: %v", err)
	}
	deps := QueryCommandDeps{ConfigPrep: func(ctx context.Context) (string, error) {
		if err := chat.UpdateDirScopeFromCWD(ctx, confDir, "second"); err != nil {
			return "", err
		}
		return confDir, nil
	}}
	var sendErr error
	captureStdoutStderrText(t, func() {
		sendErr = ContinueSender(deps)(ctx, chat.ContinueRequest{ChatID: "first", Prompt: "Ada is Speaker A", Raw: true, Flags: []string{"-cm=mock_test"}})
	})
	if sendErr != nil {
		t.Fatalf("send: %v", sendErr)
	}
	read := func(id string) pub_models.Chat {
		c, err := chat.LoadConversation(ctx, confDir, id)
		if err != nil {
			t.Fatalf("load %s: %v", id, err)
		}
		return c
	}
	first, second := read("first"), read("second")
	if !slices.ContainsFunc(first.Messages, func(m pub_models.Message) bool { return m.Role == "user" && m.Content == "Ada is Speaker A" }) {
		t.Fatalf("the named chat did not get the prompt: %v", first.Messages)
	}
	if len(second.Messages) != 3 {
		t.Fatalf("the chat the directory binds got the prompt: %v", second.Messages)
	}
}
