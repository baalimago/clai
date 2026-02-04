package photo

import (
	"flag"
	"os"
	"path"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/chat"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func withFlagArgs(t *testing.T, args []string, fn func()) {
	old := flag.CommandLine
	t.Cleanup(func() { flag.CommandLine = old })
	flag.CommandLine = flag.NewFlagSet("test", flag.ContinueOnError)
	_ = flag.CommandLine.Parse(args)
	fn()
}

func TestSetupPrompts_ArgsOnly(t *testing.T) {
	withFlagArgs(t, []string{"cmd", "hello", "world"}, func() {
		c := &Configurations{PromptFormat: "%v"}
		if err := c.SetupPrompts(); err != nil {
			t.Fatalf("SetupPrompts error: %v", err)
		}
		if got, want := c.Prompt, "hello world"; got != want {
			t.Fatalf("got prompt %q, want %q", got, want)
		}
	})
}

func TestSetupPrompts_StdinOnly(t *testing.T) {
	// Prepare stdin pipe
	oldStdin := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = oldStdin })
	_, _ = w.WriteString("piped content")
	_ = w.Close()

	withFlagArgs(t, []string{"cmd"}, func() {
		c := &Configurations{PromptFormat: "==%v=="}
		if err := c.SetupPrompts(); err != nil {
			t.Fatalf("SetupPrompts error: %v", err)
		}
		if got, want := c.Prompt, "==piped content=="; got != want {
			t.Fatalf("got prompt %q, want %q", got, want)
		}
	})
}

func TestSetupPrompts_ReplyModePrependsMessages(t *testing.T) {
	// Point config dir to a temp directory
	tmp := t.TempDir()
	claiConfDir := path.Join(tmp, ".clai")
	t.Setenv("CLAI_CONFIG_DIR", claiConfDir)

	if err := os.MkdirAll(path.Join(claiConfDir, "conversations"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	prev := pub_models.Chat{Messages: []pub_models.Message{
		{Role: "user", Content: "hi there"},
	}}
	if err := chat.SaveAsPreviousQuery(claiConfDir, prev); err != nil {
		t.Fatalf("save prev query: %v", err)
	}

	withFlagArgs(t, []string{"cmd", "hello"}, func() {
		c := &Configurations{PromptFormat: "%v", ReplyMode: true}
		if err := c.SetupPrompts(); err != nil {
			t.Fatalf("SetupPrompts error: %v", err)
		}
		if !strings.Contains(c.Prompt, "Messages:") || !strings.Contains(c.Prompt, "\"hi there\"") {
			t.Fatalf("expected previous messages embedded, got: %q", c.Prompt)
		}
		if !strings.HasSuffix(c.Prompt, "hello") {
			t.Fatalf("expected prompt to end with formatted args, got: %q", c.Prompt)
		}
	})
}
