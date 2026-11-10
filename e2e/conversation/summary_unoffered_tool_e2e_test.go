package conversation

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/testenv/stubllm"
	"github.com/baalimago/clai/internal/tools"
)

// injectedLabeller is a model whose labelling answer first calls cmd, as a
// transcript carrying injected instructions could make it, and then submits.
// Any other request gets a plain reply.
func injectedLabeller(marker string) stubllm.Script {
	return func(body string) stubllm.Answer {
		if !strings.Contains(body, "conversation labeller") {
			return stubllm.Answer{Text: "THE-REPLY"}
		}
		switch strings.Count(body, `"role":"tool"`) {
		case 0:
			return stubllm.TouchCall("cmd", marker)
		case 1:
			return stubllm.Answer{Tool: "submit_summary", Arguments: `{"title":"Stub title","summary":"Stub summary."}`}
		default:
			return stubllm.Answer{Text: "done"}
		}
	}
}

// labellerRequests returns the labeller's requests in order.
func labellerRequests(provider *stubllm.Provider) []string {
	var out []string
	for _, body := range provider.Requests() {
		if strings.Contains(body, "conversation labeller") {
			out = append(out, body)
		}
	}
	return out
}

// assertLabellerRefusedCmd checks that no labelling run executed its cmd
// call, that each offered only submit_summary and that each was told so.
func assertLabellerRefusedCmd(t *testing.T, provider *stubllm.Provider, marker string) {
	t.Helper()
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("the labeller ran cmd: marker stat err = %v, want not exist", err)
	}
	requests := labellerRequests(provider)
	if len(requests) < 2 {
		t.Fatalf("labeller requests = %d, want the cmd call and its follow-up", len(requests))
	}
	if offered := stubllm.OfferedTools(t, requests[0]); !slices.Equal(offered, []string{"submit_summary"}) {
		t.Fatalf("labeller offered %v, want only submit_summary", offered)
	}
	if !strings.Contains(requests[1], "ERROR: unknown tool call: cmd") {
		t.Fatalf("labeller follow-up = %q, want the unknown-tool refusal", requests[1])
	}
}

func setupInjectedLabeller(t *testing.T) (confDir, marker string, provider *stubllm.Provider) {
	t.Helper()
	confDir = setupSummaryE2E(t)
	// The main run of the in-flight row, like any host that has used tools,
	// fills the process registry with every built-in.
	tools.Init(context.Background())
	marker = filepath.Join(t.TempDir(), "ran")
	provider = stubllm.Start(t, injectedLabeller(marker))
	provider.WriteModel(t, confDir, "gpt-stub")
	return confDir, marker, provider
}

// The in-flight labeller offers only submit_summary; a cmd call in its
// answer is refused and the label still lands.
func Test_e2e_query_labeller_refuses_unoffered_tool(t *testing.T) {
	confDir, marker, provider := setupInjectedLabeller(t)

	stdout, _ := runQueryCaptured(t, "-r -cm gpt-stub -sm gpt-stub -t pwd q hello")

	if !strings.Contains(stdout, "THE-REPLY") {
		t.Fatalf("stdout = %q, want the main reply", stdout)
	}
	assertLabellerRefusedCmd(t, provider, marker)
	if got := onlyConversation(t, confDir); got.Title != "Stub title" {
		t.Fatalf("title = %q, want the submitted label", got.Title)
	}
}

// chat summarize labels through the same summarizer and refuses the same call.
func Test_e2e_chat_summarize_refuses_unoffered_tool(t *testing.T) {
	confDir, marker, provider := setupInjectedLabeller(t)
	convDir := filepath.Join(confDir, "conversations")
	seedSummarizable(t, convDir, "inside", time.Now().Add(-time.Hour))

	stdout, stderr, status := runStatusCaptured(t, "c summarize -y -sm gpt-stub 7d")

	if status != 0 {
		t.Fatalf("status %d, stdout=%q stderr=%q", status, stdout, stderr)
	}
	assertLabellerRefusedCmd(t, provider, marker)
	if !strings.Contains(stdout, "inside: Stub title") {
		t.Fatalf("stdout = %q, want the submitted label", stdout)
	}
}
