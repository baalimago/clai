package summary

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/skills"
	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

// quietEnv blanks every switch that would make a test host-sensitive or
// select a paid vendor: debug traces, the kill switch and the vendor keys.
func quietEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"DEBUG", "DEBUG_SUMMARY", "DEBUG_CHAT", "DEBUG_STOPLOSS", EnvSummarizer, "OPENROUTER_API_KEY", "OPENAI_API_KEY", "ANTHROPIC_API_KEY"} {
		t.Setenv(key, "")
	}
}

// seedConfigDir mirrors the root package's setupMainTestConfigDir: the
// required directories, a theme, the mock price files, textConfig.json from
// text.Default with the mock as the ladder floor, and a skills config.
func seedConfigDir(t *testing.T) string {
	t.Helper()
	quietEnv(t)
	t.Setenv("CLAI_DISABLE_COST_ERR_LOG_GOROUTINE", "1")
	confDir := t.TempDir()
	for _, dir := range []string{"conversations", "profiles", "mcpServers", "conversations/dirs", "shellContexts", "skills"} {
		if err := os.MkdirAll(filepath.Join(confDir, dir), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", dir, err)
		}
	}
	theme := `{"primary":"","secondary":"","breadtext":"","roleSystem":"","roleUser":"","roleTool":"","roleReasoning":"","roleOther":"","notificationBell":true,"tableItems":10,"toolOutputRows":6,"rollingOutput":{"enabled":true,"windowCellHeight":30}}`
	if err := os.WriteFile(filepath.Join(confDir, "theme.json"), []byte(theme), 0o644); err != nil {
		t.Fatalf("WriteFile(theme.json): %v", err)
	}
	writePriceFiles(t, confDir)
	textConf := text.Default
	textConf.Model = "test"
	if err := utils.CreateFile(filepath.Join(confDir, "textConfig.json"), &textConf); err != nil {
		t.Fatalf("CreateFile(textConfig.json): %v", err)
	}
	if err := utils.CreateFile(filepath.Join(confDir, "skills.json"), &skills.Config{
		Enabled:            false,
		GlobalSkillDirs:    []string{},
		ProjectSkillDirs:   []string{"./agents/skills", ".claude/skills"},
		TrustAllSkills:     false,
		MaxActivatedSkills: 10,
	}); err != nil {
		t.Fatalf("CreateFile(skills.json): %v", err)
	}
	t.Setenv("CLAI_CONFIG_DIR", confDir)
	return confDir
}

func writePriceFiles(t *testing.T, confDir string) {
	t.Helper()
	price, err := json.Marshal(map[string]any{"price": map[string]any{
		"input_usd_per_token":        0.001,
		"input_cached_usd_per_token": 0.0005,
		"output_usd_per_token":       0.002,
	}})
	if err != nil {
		t.Fatalf("Marshal(price): %v", err)
	}
	for _, name := range []string{"mock_test_test.json", "mock_test_mock_test.json"} {
		if err := os.WriteFile(filepath.Join(confDir, name), price, 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", name, err)
		}
	}
}

func newTestSummarizer(t *testing.T, confDir string) *agentSummarizer {
	t.Helper()
	s, err := NewAgentSummarizer(confDir)
	if err != nil {
		t.Fatalf("NewAgentSummarizer: %v", err)
	}
	return s.(*agentSummarizer)
}

func mockChat(tokens int) pub_models.Chat {
	return pub_models.Chat{ID: "conv-1", Messages: []pub_models.Message{
		{Role: "user", Content: "how do I list files " + strings.Repeat("tool_submit_summary ", tokens)},
		{Role: "assistant", Content: "Use ls."},
	}}
}

func mockRequest(tokens int) models.SummaryRequest {
	return models.SummaryRequest{Chat: mockChat(tokens), Model: "test"}
}

func configDirFiles(t *testing.T, dir string) []string {
	t.Helper()
	var entries []string
	if err := fs.WalkDir(os.DirFS(dir), ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			entries = append(entries, "/"+p)
		}
		return err
	}); err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return entries
}

// chatCapture wraps the real querier factory so a test can inspect the
// querier chat the run produced while the whole path stays real.
type chatCapture struct {
	models.ChatQuerier
	chat pub_models.Chat
}

func (c *chatCapture) TextQuery(ctx context.Context, chat pub_models.Chat) (pub_models.Chat, error) {
	out, err := c.ChatQuerier.TextQuery(ctx, chat)
	c.chat = out
	return out, err
}

func captureQuerierChat(s *agentSummarizer) *chatCapture {
	capture := &chatCapture{}
	s.newQuerier = func(ctx context.Context, conf text.Configurations) (models.ChatQuerier, error) {
		q, err := createChatQuerier(ctx, conf)
		if err != nil {
			return nil, err
		}
		capture.ChatQuerier = q
		return capture, nil
	}
	return capture
}

// submittingQuerier stands in for a model that calls submit_summary with the
// given inputs and then ends its run with err; when block is set it holds the
// run open after submitting until the context is cancelled.
type submittingQuerier struct {
	tool  pub_models.LLMTool
	input pub_models.Input
	err   error
	block bool
	seen  chan struct{}
}

func (s *submittingQuerier) Query(ctx context.Context) error {
	if s.input != nil {
		if _, err := s.tool.Call(s.input); err != nil {
			return err
		}
	}
	if s.block {
		close(s.seen)
		<-ctx.Done()
		return ctx.Err()
	}
	return s.err
}

func (s *submittingQuerier) TextQuery(ctx context.Context, _ pub_models.Chat) (pub_models.Chat, error) {
	return pub_models.Chat{}, s.Query(ctx)
}

func installSubmittingQuerier(s *agentSummarizer, sq *submittingQuerier) {
	s.newQuerier = func(_ context.Context, conf text.Configurations) (models.ChatQuerier, error) {
		sq.tool = conf.Tools[0]
		return sq, nil
	}
}

// querierOnly satisfies models.Querier and nothing more.
type querierOnly struct{}

func (querierOnly) Query(context.Context) error { return nil }

func toolMessages(chat pub_models.Chat) []string {
	var out []string
	for _, msg := range chat.Messages {
		if msg.Role == "tool" {
			out = append(out, msg.Content)
		}
	}
	return out
}

func TestResolveModel_ladder(t *testing.T) {
	withQueries := pub_models.Chat{Queries: []pub_models.QueryCost{
		{Model: "older"},
		{Model: "newest"},
		{Model: ""},
		{Model: "summary-run", Purpose: "summary"},
	}}
	tests := []struct {
		name          string
		explicit      string
		summaryModel  string
		chat          pub_models.Chat
		configModel   string
		want, wantErr string
	}{
		{"explicit wins", "flag", "cfg", withQueries, "text", "flag", ""},
		{"config summary-model", "", "cfg", withQueries, "text", "cfg", ""},
		{"last recorded model skips empty and summary rows", "", "", withQueries, "text", "newest", ""},
		{"text config model", "", "", pub_models.Chat{}, "text", "text", ""},
		{"whitespace counts as empty", " ", " ", pub_models.Chat{}, " ", "", "no model"},
		{"every rung empty", "", "", pub_models.Chat{}, "", "", "no model"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveModel(tc.explicit, tc.summaryModel, tc.chat, tc.configModel)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("resolveModel = (%q, %v), want %q", got, err, tc.want)
			}
		})
	}
}

func TestAgentSummarizer_submitsThroughMock(t *testing.T) {
	confDir := seedConfigDir(t)
	s := newTestSummarizer(t, confDir)
	got, err := s.Summarize(context.Background(), mockRequest(1))
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if got.Title != "Mock title" || got.Summary != "Mock summary." {
		t.Fatalf("result = %+v, want the mock defaults", got)
	}
	if got.Model != "test" {
		t.Fatalf("Model = %q, want test", got.Model)
	}
	if len(got.Queries) != 1 || got.Queries[0].Purpose != "summary" || got.Queries[0].Usage.TotalTokens == 0 {
		t.Fatalf("Queries = %+v, want one summary row with usage", got.Queries)
	}
	if got.GeneratedAt.IsZero() || got.GeneratedAt.Location() != time.UTC {
		t.Fatalf("GeneratedAt = %v, want a UTC stamp", got.GeneratedAt)
	}
}

func TestAgentSummarizer_retriesAfterRejection(t *testing.T) {
	confDir := seedConfigDir(t)
	t.Setenv("CLAI_MOCK_SUMMARY_TITLES", strings.Repeat("x", 70)+"|Good title")
	s := newTestSummarizer(t, confDir)
	capture := captureQuerierChat(s)
	got, err := s.Summarize(context.Background(), mockRequest(2))
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if got.Title != "Good title" {
		t.Fatalf("Title = %q, want the second submission", got.Title)
	}
	results := toolMessages(capture.chat)
	if len(results) != 2 {
		t.Fatalf("tool results = %v, want two", results)
	}
	if !strings.Contains(results[0], "ERROR:") || !strings.Contains(results[0], "title:") {
		t.Fatalf("first tool result = %q, want a rejection naming title", results[0])
	}
	if !strings.HasSuffix(results[1], "accepted") {
		t.Fatalf("second tool result = %q, want accepted", results[1])
	}
}

func TestAgentSummarizer_errorsWithoutSubmission(t *testing.T) {
	confDir := seedConfigDir(t)
	s := newTestSummarizer(t, confDir)

	t.Run("submission rejected", func(t *testing.T) {
		t.Setenv("CLAI_MOCK_SUMMARY_TITLES", strings.Repeat("x", 70))
		_, err := s.Summarize(context.Background(), mockRequest(1))
		if err == nil || !strings.Contains(err.Error(), "no summary submitted") {
			t.Fatalf("err = %v, want no summary submitted", err)
		}
	})
	t.Run("model never calls the tool", func(t *testing.T) {
		_, err := s.Summarize(context.Background(), mockRequest(0))
		if err == nil || !strings.Contains(err.Error(), "no summary submitted") {
			t.Fatalf("err = %v, want no summary submitted", err)
		}
	})
}

func TestAgentSummarizer_exhaustsAttempts(t *testing.T) {
	confDir := seedConfigDir(t)
	t.Setenv("CLAI_MOCK_SUMMARY_TITLES", strings.Repeat("x", 70))
	s := newTestSummarizer(t, confDir)
	capture := captureQuerierChat(s)
	// Four rejected attempts, then the three-step refusal ladder and the
	// hard stop: eight scripted calls end the run without a result.
	_, err := s.Summarize(context.Background(), mockRequest(MaxToolCalls*2))
	if err == nil || !strings.Contains(err.Error(), "no summary submitted") {
		t.Fatalf("err = %v, want no summary submitted", err)
	}
	results := toolMessages(capture.chat)
	if len(results) != MaxToolCalls*2 {
		t.Fatalf("tool results = %d, want %d", len(results), MaxToolCalls*2)
	}
	for i, res := range results[:MaxToolCalls] {
		if !strings.Contains(res, "title:") {
			t.Fatalf("attempt %d = %q, want a validation rejection", i, res)
		}
	}
	for i, res := range results[MaxToolCalls:] {
		if !strings.Contains(res, "No more tool calls allowed") {
			t.Fatalf("refusal %d = %q, want the ladder", i, res)
		}
	}
}

func TestAgentSummarizer_isSideEffectFree(t *testing.T) {
	confDir := seedConfigDir(t)
	// The manager's error goroutine runs here; its writer is the trace.
	t.Setenv("CLAI_DISABLE_COST_ERR_LOG_GOROUTINE", "")
	marker := filepath.Join(t.TempDir(), "ambient-spawned")
	server := fmt.Sprintf(`{"command":"sh","args":["-c","touch %s"]}`, marker)
	if err := os.WriteFile(filepath.Join(confDir, "mcpServers", "ambient.json"), []byte(server), 0o644); err != nil {
		t.Fatalf("write ambient server: %v", err)
	}
	chat.SkipIndex = false
	s := newTestSummarizer(t, confDir)
	before := configDirFiles(t, confDir)

	var got models.Summary
	var err error
	var stderr string
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		stderr = testboil.CaptureStderr(t, func(t *testing.T) {
			got, err = s.Summarize(context.Background(), mockRequest(1))
		})
	})
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if got.Title != "Mock title" {
		t.Fatalf("Title = %q", got.Title)
	}
	if stdout != "" || stderr != "" {
		t.Fatalf("stdout = %q, stderr = %q, want both empty", stdout, stderr)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("ambient MCP server must not spawn, marker stat: %v", statErr)
	}
	if chat.SkipIndex {
		t.Fatalf("chat.SkipIndex must stay false")
	}
	after := configDirFiles(t, confDir)
	if !slices.Equal(before, after) {
		t.Fatalf("config dir changed:\nbefore %v\nafter  %v", before, after)
	}
	for _, forbidden := range []string{"/conversations/", "globalScope.json", "chat_index.cache", "/dirs/"} {
		for _, entry := range after {
			if strings.Contains(entry, forbidden) {
				t.Fatalf("summarizer wrote %q", entry)
			}
		}
	}
}

func TestAgentSummarizer_stopEventDoesNotCancelCaller(t *testing.T) {
	confDir := seedConfigDir(t)
	s := newTestSummarizer(t, confDir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	callerCancelled := false
	ctx = context.WithValue(ctx, utils.ContextCancelKey, context.CancelFunc(func() {
		callerCancelled = true
		cancel()
	}))
	got, err := s.Summarize(ctx, mockRequest(1))
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if got.Title != "Mock title" {
		t.Fatalf("Title = %q", got.Title)
	}
	if ctx.Err() != nil {
		t.Fatalf("caller context done after Summarize: %v", ctx.Err())
	}
	if callerCancelled {
		t.Fatalf("the caller's cancel func must never be invoked")
	}
}

// blockingQuerier stands in for a model call in flight: it returns only when
// the context it was handed is cancelled.
type blockingQuerier struct{ seen chan struct{} }

func (b *blockingQuerier) Query(ctx context.Context) error {
	close(b.seen)
	<-ctx.Done()
	return ctx.Err()
}

func (b *blockingQuerier) TextQuery(ctx context.Context, chat pub_models.Chat) (pub_models.Chat, error) {
	return pub_models.Chat{}, b.Query(ctx)
}

func TestAgentSummarizer_cancelled(t *testing.T) {
	confDir := seedConfigDir(t)

	t.Run("before the call", func(t *testing.T) {
		s := newTestSummarizer(t, confDir)
		before := configDirFiles(t, confDir)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := s.Summarize(ctx, mockRequest(1))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		if after := configDirFiles(t, confDir); !slices.Equal(before, after) {
			t.Fatalf("config dir changed: %v -> %v", before, after)
		}
	})

	t.Run("while in flight", func(t *testing.T) {
		s := newTestSummarizer(t, confDir)
		bq := &blockingQuerier{seen: make(chan struct{})}
		s.newQuerier = func(context.Context, text.Configurations) (models.ChatQuerier, error) { return bq, nil }
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			_, err := s.Summarize(ctx, mockRequest(1))
			done <- err
		}()
		<-bq.seen
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("err = %v, want context.Canceled", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("Summarize did not return after the caller cancelled")
		}
	})
}

func TestAgentSummarizer_queriesCarryUsage(t *testing.T) {
	confDir := seedConfigDir(t)
	s := newTestSummarizer(t, confDir)
	got, err := s.Summarize(context.Background(), mockRequest(1))
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if len(got.Queries) == 0 {
		t.Fatalf("no Queries rows")
	}
	for i, row := range got.Queries {
		if row.Purpose != "summary" {
			t.Fatalf("row %d Purpose = %q, want summary", i, row.Purpose)
		}
		if row.Usage.TotalTokens == 0 || row.CostUSD <= 0 {
			t.Fatalf("row %d = %+v, want usage and cost from the seeded price", i, row)
		}
	}
}

func TestAgentSummarizer_synthesizesUsageRow(t *testing.T) {
	confDir := seedConfigDir(t)
	// Without a price file the catalog never becomes usable, so enrichment
	// yields no row and the summarizer synthesizes one from recorded usage.
	for _, name := range []string{"mock_test_test.json", "mock_test_mock_test.json"} {
		if err := os.Remove(filepath.Join(confDir, name)); err != nil {
			t.Fatalf("Remove(%q): %v", name, err)
		}
	}
	s := newTestSummarizer(t, confDir)
	got, err := s.Summarize(context.Background(), mockRequest(1))
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if len(got.Queries) != 1 {
		t.Fatalf("Queries = %+v, want one synthesized row", got.Queries)
	}
	row := got.Queries[0]
	if row.Purpose != "summary" || row.Model != "test" || row.CostUSD != 0 || row.Usage.TotalTokens == 0 || row.CreatedAt.IsZero() {
		t.Fatalf("synthesized row = %+v", row)
	}
}

func TestAgentSummarizer_unknownModel(t *testing.T) {
	confDir := seedConfigDir(t)
	s := newTestSummarizer(t, confDir)
	req := mockRequest(1)
	req.Model = "no-such-vendor-model"
	_, err := s.Summarize(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "no-such-vendor-model") {
		t.Fatalf("err = %v, want the model name", err)
	}
}

func TestNewAgentSummarizer_loadsConfig(t *testing.T) {
	quietEnv(t)
	t.Run("absent file is created silently with the default model as floor", func(t *testing.T) {
		confDir := t.TempDir()
		var s models.Summarizer
		var err error
		var stderr string
		stdout := testboil.CaptureStdout(t, func(t *testing.T) {
			stderr = testboil.CaptureStderr(t, func(t *testing.T) {
				s, err = NewAgentSummarizer(confDir)
			})
		})
		if err != nil {
			t.Fatalf("NewAgentSummarizer: %v", err)
		}
		if stdout != "" || stderr != "" {
			t.Fatalf("stdout = %q, stderr = %q, want both empty", stdout, stderr)
		}
		if as := s.(*agentSummarizer); as.configModel != text.Default.Model || as.summaryModel != text.Default.SummaryModel {
			t.Fatalf("loaded = %+v, want the defaults as floor", as)
		}
		if _, err := os.Stat(filepath.Join(confDir, "textConfig.json")); err != nil {
			t.Fatalf("default file must be created: %v", err)
		}
	})

	t.Run("malformed JSON returns the unmarshal error", func(t *testing.T) {
		confDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(confDir, "textConfig.json"), []byte(`{"model": "test",`), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		_, err := NewAgentSummarizer(confDir)
		if err == nil || !strings.Contains(err.Error(), "unmarshal") || !strings.Contains(err.Error(), "textConfig.json") {
			t.Fatalf("err = %v, want the unmarshal error naming textConfig.json", err)
		}
	})

	// Phase 8 (R2-04): the constructor announces an upgrade it performs;
	// the query path stays silent because SetupQuerier upgraded first.
	t.Run("file predating keys announces the upgrade once", func(t *testing.T) {
		confDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(confDir, "textConfig.json"), []byte(`{"model":"test"}`), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		var s models.Summarizer
		var err error
		stdout := testboil.CaptureStdout(t, func(t *testing.T) {
			s, err = NewAgentSummarizer(confDir)
		})
		if err != nil {
			t.Fatalf("NewAgentSummarizer: %v", err)
		}
		if !strings.Contains(stdout, "added new field(s) to textConfig.json") || !strings.Contains(stdout, "summary-model") {
			t.Fatalf("stdout = %q, want one upgrade announcement naming summary-model", stdout)
		}
		as := s.(*agentSummarizer)
		if as.configModel != "test" || as.summaryModel != "" || as.confDir != confDir {
			t.Fatalf("loaded = %+v", as)
		}
		regenerated, err := os.ReadFile(filepath.Join(confDir, "textConfig.json"))
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if !strings.Contains(string(regenerated), "stoploss") {
			t.Fatalf("defaults must be filled into the file:\n%s", regenerated)
		}
	})

	t.Run("summary-model key is kept", func(t *testing.T) {
		confDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(confDir, "textConfig.json"), []byte(`{"model":"test","summary-model":"tiny"}`), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		s, err := NewAgentSummarizer(confDir)
		if err != nil {
			t.Fatalf("NewAgentSummarizer: %v", err)
		}
		if as := s.(*agentSummarizer); as.summaryModel != "tiny" {
			t.Fatalf("summaryModel = %q, want tiny", as.summaryModel)
		}
	})

	t.Run("unreadable file", func(t *testing.T) {
		confDir := t.TempDir()
		if err := os.Mkdir(filepath.Join(confDir, "textConfig.json"), 0o755); err != nil {
			t.Fatalf("Mkdir: %v", err)
		}
		if _, err := NewAgentSummarizer(confDir); err == nil {
			t.Fatalf("expected the load error")
		}
	})
}

// TestAgentSummarizer_heldSubmissionSurvivesQuerierError pins that a valid
// submission is returned even when the run ends in a querier error, that an
// error without a submission still fails, and that the caller's cancel wins
// over a held submission (review 3, R3-06).
func TestAgentSummarizer_heldSubmissionSurvivesQuerierError(t *testing.T) {
	confDir := seedConfigDir(t)
	valid := pub_models.Input{"title": "Held title", "summary": "Held summary."}
	boom := errors.New("vendor closed the stream")

	t.Run("submits then errors", func(t *testing.T) {
		s := newTestSummarizer(t, confDir)
		installSubmittingQuerier(s, &submittingQuerier{input: valid, err: boom})
		got, err := s.Summarize(context.Background(), mockRequest(1))
		if err != nil {
			t.Fatalf("Summarize: %v, want the held label", err)
		}
		if got.Title != "Held title" || got.Summary != "Held summary." || got.Model != "test" {
			t.Fatalf("result = %+v, want the held submission", got)
		}
		if len(got.Queries) != 1 || got.Queries[0].Purpose != "summary" {
			t.Fatalf("Queries = %+v, want one synthesized summary row", got.Queries)
		}
	})

	t.Run("submits then errors traces the dropped error on stderr", func(t *testing.T) {
		t.Setenv("DEBUG_SUMMARY", "1")
		s := newTestSummarizer(t, confDir)
		installSubmittingQuerier(s, &submittingQuerier{input: valid, err: boom})
		var stderr string
		var err error
		stdout := testboil.CaptureStdout(t, func(t *testing.T) {
			stderr = testboil.CaptureStderr(t, func(t *testing.T) {
				_, err = s.Summarize(context.Background(), mockRequest(1))
			})
		})
		if err != nil {
			t.Fatalf("Summarize: %v", err)
		}
		if stdout != "" || !strings.Contains(stderr, "[DEBUG_SUMMARY]") || !strings.Contains(stderr, boom.Error()) {
			t.Fatalf("stdout = %q, stderr = %q, want the dropped error traced on stderr only", stdout, stderr)
		}
	})

	t.Run("rejected submission then error", func(t *testing.T) {
		s := newTestSummarizer(t, confDir)
		sq := &submittingQuerier{input: pub_models.Input{"title": strings.Repeat("x", 70), "summary": "s"}}
		installSubmittingQuerier(s, sq)
		_, err := s.Summarize(context.Background(), mockRequest(1))
		if err == nil || !strings.Contains(err.Error(), "title: at most") {
			t.Fatalf("err = %v, want the rejection surfaced as the run's error", err)
		}
	})

	t.Run("errors without submitting", func(t *testing.T) {
		s := newTestSummarizer(t, confDir)
		installSubmittingQuerier(s, &submittingQuerier{err: boom})
		_, err := s.Summarize(context.Background(), mockRequest(1))
		if !errors.Is(err, boom) || !strings.Contains(err.Error(), `summarize with "test"`) {
			t.Fatalf("err = %v, want the querier error", err)
		}
	})

	t.Run("caller cancel wins over a held submission", func(t *testing.T) {
		s := newTestSummarizer(t, confDir)
		sq := &submittingQuerier{input: valid, block: true, seen: make(chan struct{})}
		installSubmittingQuerier(s, sq)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			_, err := s.Summarize(ctx, mockRequest(1))
			done <- err
		}()
		<-sq.seen
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("err = %v, want context.Canceled", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("Summarize did not return after the caller cancelled")
		}
	})
}

// TestAsChatQuerier covers the seam's type assertion with a Querier-only
// fake (review 3, R3-15).
func TestAsChatQuerier(t *testing.T) {
	if _, err := asChatQuerier(querierOnly{}); err == nil || !strings.Contains(err.Error(), "is not a ChatQuerier") {
		t.Fatalf("err = %v, want the type error", err)
	}
	bq := &blockingQuerier{seen: make(chan struct{})}
	got, err := asChatQuerier(bq)
	if err != nil || got != models.ChatQuerier(bq) {
		t.Fatalf("asChatQuerier = (%v, %v), want the same querier", got, err)
	}
}

// TestTraceCostWarnf_stderr pins that the summarizer's trace lands on
// stderr, never in the answer stream, and stays silent without the flag
// (review 3, R3-17).
func TestTraceCostWarnf_stderr(t *testing.T) {
	quietEnv(t)
	capture := func(t *testing.T) (string, string) {
		var stderr string
		stdout := testboil.CaptureStdout(t, func(t *testing.T) {
			stderr = testboil.CaptureStderr(t, func(t *testing.T) {
				traceCostWarnf("manager error: %v", "boom")
			})
		})
		return stdout, stderr
	}
	t.Run("DEBUG_SUMMARY=1 writes stderr only", func(t *testing.T) {
		t.Setenv("DEBUG_SUMMARY", "1")
		stdout, stderr := capture(t)
		if stdout != "" || stderr != "[DEBUG_SUMMARY] cost: manager error: boom\n" {
			t.Fatalf("stdout = %q, stderr = %q", stdout, stderr)
		}
	})
	t.Run("silent without the flag", func(t *testing.T) {
		stdout, stderr := capture(t)
		if stdout != "" || stderr != "" {
			t.Fatalf("stdout = %q, stderr = %q, want both empty", stdout, stderr)
		}
	})
}
