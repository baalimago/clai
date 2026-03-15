package text

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/models"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

type MockQuerier struct {
	Somefield   string `json:"somefield"`
	shouldBlock bool
	usage       *pub_models.Usage
	modelName   string
	streamFn    func(
		context.Context,
		pub_models.Chat,
	) (chan models.CompletionEvent, error)
	// completionChan is used to simulate a stream of completions
	// send 'CLOSE' outChan, used in tests, plus the MockQuerier
	completionChan chan models.CompletionEvent
	// errChan is used to simulate a stream of errors. Send
	errChan chan error
}

func (q *MockQuerier) Setup() error {
	return nil
}

func (q *MockQuerier) TokenUsage() *pub_models.Usage {
	return q.usage
}

func (q *MockQuerier) ModelName() string {
	return q.modelName
}

func (q *MockQuerier) StreamCompletions(ctx context.Context, chat pub_models.Chat) (chan models.CompletionEvent, error) {
	if q.streamFn != nil {
		return q.streamFn(ctx, chat)
	}
	// simulate a stream of completions via the sendChan, so that
	// it's possible to send messages from outside the test
	if q.completionChan != nil {
		outChan := make(chan models.CompletionEvent)
		go func() {
			if q.shouldBlock {
				ch := make(chan struct{})
				<-ch
			}
			for {
				select {
				case <-ctx.Done():
					return
				case err := <-q.errChan:
					outChan <- models.CompletionEvent(err)
				case msg := <-q.completionChan:
					if msg == "CLOSE" {
						close(outChan)
						return
					}
					outChan <- models.CompletionEvent(msg)
				}
			}
		}()
		return outChan, nil
	}
	return nil, nil
}

func Test_Querier_NewQuerier(t *testing.T) {
	t.Run("it should load local model with correct type", func(t *testing.T) {
		want := "somevalue"
		model := "mock"
		savedModel := MockQuerier{
			Somefield: want,
		}
		bytes, err := json.Marshal(savedModel)
		if err != nil {
			t.Fatalf("failed to marshal saveModel: %v", err)
		}
		tmpDir := t.TempDir()
		os.Mkdir(path.Join(tmpDir, ".clai"), os.FileMode(0o755))
		err = os.WriteFile(path.Join(tmpDir, ".clai", fmt.Sprintf("%v_%v_%v.json", model, model, model)), bytes, os.FileMode(0o755))
		if err != nil {
			t.Fatalf("failed to write mock savedModel: %v", err)
		}
		conf := Configurations{
			Model:     model,
			ConfigDir: path.Join(tmpDir, ".clai"),
		}

		// Here we want to ensure that using only the conf + the type of the model,
		// we get the correct querier back
		q, err := NewQuerier(context.Background(), conf, &MockQuerier{})
		if err != nil {
			t.Errorf("got error: %v", err)
		}
		if q.Model == nil {
			t.Errorf("expected model to be set")
		}
		if q.Model.Somefield != want {
			t.Error("expected Model to be of type *MockQuerier")
		}
	})
}

func Test_Querier_handleToken(t *testing.T) {
	t.Run("it should print to stdout", func(t *testing.T) {
		querier := Querier[*MockQuerier]{}
		want := "somevalue"

		got := testboil.CaptureStdout(t, func(t *testing.T) {
			t.Helper()
			querier.handleToken(want)
		})

		if got != want {
			t.Fatalf("expected: %v, got: %v", want, got)
		}
	})
}

func Test_Context(t *testing.T) {
	q := Querier[*MockQuerier]{
		Model: &MockQuerier{
			shouldBlock: true,
		},
	}
	testboil.ReturnsOnContextCancel(t, func(ctx context.Context) {
		q.Query(ctx)
	}, time.Second)
}

func Test_Querier_eventHandling(t *testing.T) {
	testCases := []struct {
		desc  string
		q     Querier[*MockQuerier]
		given []models.CompletionEvent
		want  string
	}{
		{
			desc: "it should write token to writer",
			q: Querier[*MockQuerier]{
				Raw: true,
				Model: &MockQuerier{
					completionChan: make(
						chan models.CompletionEvent),
				},
				out: &strings.Builder{},
			},
			given: []models.CompletionEvent{
				"test", "CLOSE",
			},
			want: "test",
		},
		{
			desc: "token whitespace should be " +
				"respected",
			q: Querier[*MockQuerier]{
				Raw: true,
				Model: &MockQuerier{
					completionChan: make(
						chan models.CompletionEvent),
				},
				out: &strings.Builder{},
			},
			given: []models.CompletionEvent{
				" one", "two\n", "  three ",
				"CLOSE",
			},
			want: " onetwo\n  three ",
		},
		{
			desc: "it should call test function " +
				"when asked to",
			q: Querier[*MockQuerier]{
				Raw: true,
				Model: &MockQuerier{
					completionChan: make(
						chan models.CompletionEvent),
				},
				out: &strings.Builder{},
			},
			given: []models.CompletionEvent{
				"first the model said something",
				pub_models.Call{
					Name: "test",
					Inputs: &pub_models.Input{
						"testKey": "testVal",
					},
				},
				"CLOSE",
			},
			want: "Call: 'test', inputs: [ " +
				"'testKey': 'testVal' ]",
		},
	}
	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			go func() {
				for _, msg := range tC.given {
					tC.q.Model.completionChan <- msg
				}
			}()

			err := tC.q.Query(context.Background())
			if err != nil {
				t.Fatalf("Query returned err: %v", err)
			}

			b, ok := tC.q.out.(*strings.Builder)
			if !ok {
				t.Fatalf("expected out to be *strings.Builder")
			}
			got := b.String()

			if !strings.Contains(got, tC.want) {
				t.Fatalf("expected: %q, got: %q",
					tC.want, got)
			}
		})
	}
}

func Test_Querier_Query_errors(t *testing.T) {
	rcMu := sync.Mutex{}
	rcMu.Lock()
	testCases := []struct {
		desc  string
		q     Querier[*MockQuerier]
		given error
		want  error
	}{
		{
			desc: "given EOF it should exit without print",
			q: Querier[*MockQuerier]{
				Raw: true,
				out: os.Stdout,
				Model: &MockQuerier{
					completionChan: make(chan models.CompletionEvent),
					errChan:        make(chan error),
				},
			},
			given: io.EOF,
			want:  nil,
		},
		{
			desc: "given context cancel error it should exit without print",
			q: Querier[*MockQuerier]{
				Raw: true,
				out: os.Stdout,
				Model: &MockQuerier{
					completionChan: make(chan models.CompletionEvent),
					errChan:        make(chan error),
				},
			},
			given: context.Canceled,
			want:  nil,
		},
		{
			desc: "on some other error, the error should be printed",
			q: Querier[*MockQuerier]{
				Raw: true,
				out: os.Stdout,
				Model: &MockQuerier{
					completionChan: make(chan models.CompletionEvent),
					errChan:        make(chan error),
				},
			},
			given: errors.New("some other error"),
			want:  errors.New("some other error"),
		},
	}
	rcMu.Unlock()
	for _, tC := range testCases {
		// Race flag being pissy for minimal reasons
		rcMu.Lock()
		tC := tC
		rcMu.Unlock()
		t.Run(tC.desc, func(t *testing.T) {
			go func() {
				rcMu.Lock()
				defer rcMu.Unlock()
				tC.q.Model.errChan <- tC.given
				tC.q.Model.completionChan <- "CLOSE"
			}()

			got := tC.q.Query(context.Background())

			if got == nil {
				if tC.want != nil {
					t.Fatalf("expected: %v, got: %v", tC.want, got)
				}
			} else {
				if !strings.Contains(got.Error(), tC.want.Error()) {
					t.Fatalf("expected: %v, got: %v", tC.want, got.Error())
				}
			}
		})
	}
}

func Test_Querier(t *testing.T) {
	t.Run("it should only save to globalScope if in reply mode", func(t *testing.T) {
		tmpConfigDir := path.Join(t.TempDir(), ".clai")
		os.MkdirAll(path.Join(tmpConfigDir, "conversations"), os.ModePerm)
		q := Querier[*MockQuerier]{
			Raw:             true,
			out:             os.Stdout,
			shouldSaveReply: true,
			configDir:       tmpConfigDir,
			chat: pub_models.Chat{
				ID: "globalScope",
				Messages: []pub_models.Message{
					{
						Role:    "system",
						Content: "some system message",
					},
				},
			},
			Model: &MockQuerier{
				shouldBlock:    false,
				completionChan: make(chan models.CompletionEvent),
				errChan:        make(chan error),
			},
		}
		want := "something to remember"
		go func() {
			q.Model.completionChan <- want
			q.Model.completionChan <- "CLOSE"
		}()
		err := q.Query(context.Background())
		if err != nil {
			t.Fatalf("didn't expect error: %v", err)
		}
		lastReply, err := chat.LoadPrevQuery(q.configDir)
		if err != nil {
			t.Fatal(err)
		}
		if len(lastReply.Messages) != 2 {
			t.Fatalf("expected length 2, got: %v, data: %v", len(lastReply.Messages), lastReply.Messages)
		}
		if lastReply.Messages[1].Content != want {
			t.Fatalf("expected: %v, got: %v", want, lastReply.Messages[1].Content)
		}

		// Redo test with replyMode false
		q.shouldSaveReply = false
		go func() {
			q.Model.completionChan <- want
			q.Model.completionChan <- "CLOSE"
		}()
		err = q.Query(context.Background())
		if err != nil {
			t.Fatalf("didn't expect error: %v", err)
		}
		lastReply, err = chat.LoadPrevQuery(q.configDir)
		if err != nil {
			t.Fatal(err)
		}
		if len(lastReply.Messages) != 2 {
			t.Fatalf("expected length 2, got: %v, data: %v", len(lastReply.Messages), lastReply.Messages)
		}
		if lastReply.Messages[1].Content != want {
			t.Fatalf("expected: %v, got: %v", want, lastReply.Messages[1].Content)
		}
	})
}

func Test_Querier_SavesConversationOnError(t *testing.T) {
	t.Run("it should save conversation even when query returns an error with no tokens", func(t *testing.T) {
		tmpConfigDir := path.Join(t.TempDir(), ".clai")
		os.MkdirAll(path.Join(tmpConfigDir, "conversations"), os.ModePerm)
		q := Querier[*MockQuerier]{
			Raw:             true,
			out:             &strings.Builder{},
			shouldSaveReply: true,
			configDir:       tmpConfigDir,
			chat: pub_models.Chat{
				ID: "globalScope",
				Messages: []pub_models.Message{
					{
						Role:    "system",
						Content: "you are a helpful assistant",
					},
					{
						Role:    "user",
						Content: "hello world",
					},
				},
			},
			Model: &MockQuerier{
				shouldBlock:    false,
				completionChan: make(chan models.CompletionEvent),
				errChan:        make(chan error),
			},
		}

		// Send an error immediately without any tokens
		go func() {
			q.Model.errChan <- errors.New("API connection failed")
			q.Model.completionChan <- "CLOSE"
		}()

		err := q.Query(context.Background())
		if err == nil {
			t.Fatal("expected error from Query")
		}

		// The conversation should still be saved despite the error
		lastReply, err := chat.LoadPrevQuery(q.configDir)
		if err != nil {
			t.Fatalf("failed to load prev query: %v", err)
		}
		// Should have the original messages (system + user) preserved
		if len(lastReply.Messages) < 2 {
			t.Fatalf("expected at least 2 messages to be saved, got: %v, data: %v", len(lastReply.Messages), lastReply.Messages)
		}
		if lastReply.Messages[1].Content != "hello world" {
			t.Fatalf("expected user message to be preserved, got: %v", lastReply.Messages[1].Content)
		}
	})

	t.Run("it should save conversation with partial content on error", func(t *testing.T) {
		tmpConfigDir := path.Join(t.TempDir(), ".clai")
		os.MkdirAll(path.Join(tmpConfigDir, "conversations"), os.ModePerm)
		q := Querier[*MockQuerier]{
			Raw:             true,
			out:             &strings.Builder{},
			shouldSaveReply: true,
			configDir:       tmpConfigDir,
			chat: pub_models.Chat{
				ID: "globalScope",
				Messages: []pub_models.Message{
					{
						Role:    "system",
						Content: "you are a helpful assistant",
					},
					{
						Role:    "user",
						Content: "hello world",
					},
				},
			},
			Model: &MockQuerier{
				shouldBlock:    false,
				completionChan: make(chan models.CompletionEvent),
				errChan:        make(chan error),
			},
		}

		// Send some tokens, then an error
		go func() {
			q.Model.completionChan <- "partial response"
			q.Model.errChan <- errors.New("connection dropped")
			q.Model.completionChan <- "CLOSE"
		}()

		err := q.Query(context.Background())
		if err == nil {
			t.Fatal("expected error from Query")
		}

		// The conversation should be saved with the partial content
		lastReply, err := chat.LoadPrevQuery(q.configDir)
		if err != nil {
			t.Fatalf("failed to load prev query: %v", err)
		}
		// Should have original messages + the partial assistant response
		if len(lastReply.Messages) < 3 {
			t.Fatalf("expected at least 3 messages (system + user + partial), got: %v, data: %v", len(lastReply.Messages), lastReply.Messages)
		}
		if lastReply.Messages[2].Content != "partial response" {
			t.Fatalf("expected partial response to be saved, got: %v", lastReply.Messages[2].Content)
		}
	})
}

func Test_Querier_SavesConversation_WhenStreamSetupFailsDueToRateLimitTokenCount(t *testing.T) {
	tmpConfigDir := path.Join(t.TempDir(), ".clai")
	if err := os.MkdirAll(path.Join(tmpConfigDir, "conversations"), os.ModePerm); err != nil {
		t.Fatalf("mkdir conversations: %v", err)
	}

	q := Querier[*MockQuerierRateLimitTokenCountFail]{
		Raw:             true,
		out:             &strings.Builder{},
		shouldSaveReply: true,
		configDir:       tmpConfigDir,
		chat: pub_models.Chat{
			ID: "globalScope",
			Messages: []pub_models.Message{
				{Role: "system", Content: "you are a helpful assistant"},
				{Role: "user", Content: "please do the thing"},
			},
		},
		Model: &MockQuerierRateLimitTokenCountFail{},
	}

	err := q.Query(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "failed to count tokens") {
		t.Fatalf("expected token count error, got: %v", err)
	}

	// Even though stream setup failed, we should persist globalScope in reply mode.
	lastReply, err := chat.LoadPrevQuery(q.configDir)
	if err != nil {
		t.Fatalf("load prev query: %v", err)
	}
	if len(lastReply.Messages) < 2 {
		t.Fatalf("expected at least 2 messages saved, got: %v, data: %v", len(lastReply.Messages), lastReply.Messages)
	}
	if lastReply.Messages[1].Content != "please do the thing" {
		t.Fatalf("expected user message to be preserved, got: %q", lastReply.Messages[1].Content)
	}
	if len(lastReply.Queries) != 0 {
		t.Fatalf("expected no queries in failed reply save, got: %d", len(lastReply.Queries))
	}
}

func Test_Querier_postProcess_OnlyOuterCallEnrichesChat(t *testing.T) {
	tmpConfigDir := path.Join(t.TempDir(), ".clai")
	if err := os.MkdirAll(
		path.Join(tmpConfigDir, "conversations"),
		os.ModePerm,
	); err != nil {
		t.Fatalf("mkdir conversations: %v", err)
	}

	ready := make(chan struct{})
	close(ready)
	costMgr := &mockCostManager{
		t: t,
		enrichFn: func(got pub_models.Chat) pub_models.Chat {
			if len(got.Messages) != 2 {
				t.Fatalf(
					"expected 2 messages before enrich, got: %d",
					len(got.Messages),
				)
			}
			if got.Messages[1].Content != "outer response" {
				t.Fatalf(
					"expected outer response, got: %q",
					got.Messages[1].Content,
				)
			}
			got.ID = "enriched"
			return got
		},
	}

	q := Querier[*MockQuerier]{
		Raw:             true,
		out:             &strings.Builder{},
		shouldSaveReply: true,
		configDir:       tmpConfigDir,
		callStackLevel:  2,
		costManager:     costMgr,
		costMgrRdyChan:  ready,
		chat: pub_models.Chat{
			ID: "globalScope",
			Messages: []pub_models.Message{
				{Role: "user", Content: "hello"},
			},
		},
		fullMsg: "outer response",
	}

	q.postProcess()

	if costMgr.calls != 1 {
		t.Fatalf("expected 1 enrich call for nested tool root query, got: %d", costMgr.calls)
	}

	saved, err := chat.LoadPrevQuery(tmpConfigDir)
	if err != nil {
		t.Fatalf("load prev query: %v", err)
	}
	if saved.ID != "globalScope" {
		t.Fatalf("expected saved global scope id, got: %q", saved.ID)
	}
	if len(saved.Messages) != 2 {
		t.Fatalf("expected 2 saved messages, got: %d", len(saved.Messages))
	}
	if saved.Messages[1].Content != "outer response" {
		t.Fatalf("expected saved outer response, got: %q", saved.Messages[1].Content)
	}
}

func Test_Querier_postProcess_enriches_before_save_when_cost_manager_ready(t *testing.T) {
	tmpConfigDir := path.Join(t.TempDir(), ".clai")
	if err := os.MkdirAll(
		path.Join(tmpConfigDir, "conversations"),
		os.ModePerm,
	); err != nil {
		t.Fatalf("mkdir conversations: %v", err)
	}

	ready := make(chan struct{})
	close(ready)
	enrichCalls := 0
	costMgr := &mockCostManager{
		t: t,
		enrichFn: func(got pub_models.Chat) pub_models.Chat {
			enrichCalls++
			got.Queries = append(got.Queries, pub_models.QueryCost{CostUSD: 0.42})
			return got
		},
	}

	q := Querier[*MockQuerier]{
		configDir:       tmpConfigDir,
		shouldSaveReply: true,
		costManager:     costMgr,
		costMgrRdyChan:  ready,
		chat: pub_models.Chat{
			ID: "globalScope",
			Messages: []pub_models.Message{
				{Role: "system", Content: "sys"},
				{Role: "user", Content: "hello"},
				{Role: "system", Content: "answer"},
			},
			TokenUsage: &pub_models.Usage{
				PromptTokens:     1,
				CompletionTokens: 2,
				TotalTokens:      3,
			},
		},
	}

	q.postProcess()

	if enrichCalls != 1 {
		t.Fatalf("expected 1 enrich call, got %d", enrichCalls)
	}

	savedPath := path.Join(tmpConfigDir, "conversations", "globalScope.json")
	savedBytes, err := os.ReadFile(savedPath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", savedPath, err)
	}

	var saved pub_models.Chat
	if err := json.Unmarshal(savedBytes, &saved); err != nil {
		t.Fatalf("Unmarshal(%q): %v", savedPath, err)
	}
	if len(saved.Queries) != 1 {
		t.Fatalf("expected saved queries len 1, got %d", len(saved.Queries))
	}
	if saved.Queries[0].CostUSD != 0.42 {
		t.Fatalf("expected saved cost 0.42, got %v", saved.Queries[0].CostUSD)
	}
}

func Test_Querier_postProcess_preserves_runtime_model_name_in_saved_query_cost(t *testing.T) {
	tmpConfigDir := path.Join(t.TempDir(), ".clai")
	if err := os.MkdirAll(
		path.Join(tmpConfigDir, "conversations"),
		os.ModePerm,
	); err != nil {
		t.Fatalf("mkdir conversations: %v", err)
	}

	ready := make(chan struct{})
	close(ready)
	costMgr := &mockCostManager{
		t: t,
		enrichFn: func(got pub_models.Chat) pub_models.Chat {
			got.Queries = append(got.Queries, pub_models.QueryCost{
				CostUSD:        0.42,
				MessageTrigger: 1,
				Model:          "runtime-model-b",
			})
			return got
		},
	}

	q := Querier[*MockQuerier]{
		configDir:       tmpConfigDir,
		shouldSaveReply: true,
		costManager:     costMgr,
		costMgrRdyChan:  ready,
		Model: &MockQuerier{
			modelName: "runtime-model-b",
		},
		chat: pub_models.Chat{
			ID: "globalScope",
			Messages: []pub_models.Message{
				{Role: "system", Content: "sys"},
				{Role: "user", Content: "hello"},
				{Role: "system", Content: "answer"},
			},
			TokenUsage: &pub_models.Usage{
				PromptTokens:     1,
				CompletionTokens: 2,
				TotalTokens:      3,
			},
		},
	}

	q.postProcess()

	savedPath := path.Join(tmpConfigDir, "conversations", "globalScope.json")
	savedBytes, err := os.ReadFile(savedPath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", savedPath, err)
	}

	var saved pub_models.Chat
	if err := json.Unmarshal(savedBytes, &saved); err != nil {
		t.Fatalf("Unmarshal(%q): %v", savedPath, err)
	}
	if len(saved.Queries) != 1 {
		t.Fatalf("expected saved queries len 1, got %d", len(saved.Queries))
	}
	if saved.Queries[0].Model != "runtime-model-b" {
		t.Fatalf("saved query model: got %q want %q", saved.Queries[0].Model, "runtime-model-b")
	}
}

func Test_Querier_postProcess_enriches_before_save_for_nested_tool_root_query(t *testing.T) {
	tmpConfigDir := path.Join(t.TempDir(), ".clai")
	if err := os.MkdirAll(
		path.Join(tmpConfigDir, "conversations"),
		os.ModePerm,
	); err != nil {
		t.Fatalf("mkdir conversations: %v", err)
	}

	ready := make(chan struct{})
	close(ready)
	enrichCalls := 0
	costMgr := &mockCostManager{
		t: t,
		enrichFn: func(got pub_models.Chat) pub_models.Chat {
			enrichCalls++
			got.Queries = append(got.Queries, pub_models.QueryCost{CostUSD: 0.42})
			return got
		},
	}

	q := Querier[*MockQuerier]{
		configDir:       tmpConfigDir,
		shouldSaveReply: true,
		callStackLevel:  2,
		costManager:     costMgr,
		costMgrRdyChan:  ready,
		chat: pub_models.Chat{
			ID: "globalScope",
			Messages: []pub_models.Message{
				{Role: "system", Content: "sys"},
				{Role: "user", Content: "hello"},
				{Role: "system", Content: "answer"},
			},
			TokenUsage: &pub_models.Usage{
				PromptTokens:     1,
				CompletionTokens: 2,
				TotalTokens:      3,
			},
		},
	}

	q.postProcess()

	if enrichCalls != 1 {
		t.Fatalf("expected 1 enrich call, got %d", enrichCalls)
	}

	savedPath := path.Join(tmpConfigDir, "conversations", "globalScope.json")
	savedBytes, err := os.ReadFile(savedPath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", savedPath, err)
	}

	var saved pub_models.Chat
	if err := json.Unmarshal(savedBytes, &saved); err != nil {
		t.Fatalf("Unmarshal(%q): %v", savedPath, err)
	}
	if len(saved.Queries) != 1 {
		t.Fatalf("expected saved queries len 1, got %d", len(saved.Queries))
	}
	if saved.Queries[0].CostUSD != 0.42 {
		t.Fatalf("expected saved cost 0.42, got %v", saved.Queries[0].CostUSD)
	}
}

func Test_Querier_postProcess_SkipsCostEnrichIfManagerNotReady(t *testing.T) {
	tmpConfigDir := path.Join(t.TempDir(), ".clai")
	if err := os.MkdirAll(
		path.Join(tmpConfigDir, "conversations"),
		os.ModePerm,
	); err != nil {
		t.Fatalf("mkdir conversations: %v", err)
	}

	ready := make(chan struct{})
	costMgr := &mockCostManager{
		t: t,
		enrichFn: func(got pub_models.Chat) pub_models.Chat {
			t.Fatal("did not expect Enrich to be called")
			return got
		},
	}

	q := Querier[*MockQuerier]{
		Raw:             true,
		out:             &strings.Builder{},
		shouldSaveReply: true,
		configDir:       tmpConfigDir,
		costManager:     costMgr,
		costMgrRdyChan:  ready,
		chat: pub_models.Chat{
			ID: "globalScope",
			Messages: []pub_models.Message{
				{Role: "system", Content: "sys"},
				{Role: "user", Content: "hello"},
			},
		},
		fullMsg: "answer",
	}

	start := time.Now()
	q.postProcess()
	if time.Since(start) < 190*time.Millisecond {
		t.Fatalf("expected postProcess to wait roughly for readiness timeout")
	}
	if costMgr.calls != 0 {
		t.Fatalf("expected no enrich calls, got: %d", costMgr.calls)
	}
}

func Test_Querier_postProcess_StillSavesWhenCostEnrichFails(t *testing.T) {
	tmpConfigDir := path.Join(t.TempDir(), ".clai")
	if err := os.MkdirAll(
		path.Join(tmpConfigDir, "conversations"),
		os.ModePerm,
	); err != nil {
		t.Fatalf("mkdir conversations: %v", err)
	}

	ready := make(chan struct{})
	close(ready)
	costMgr := &mockCostManager{
		t:         t,
		enrichErr: errors.New("estimate query cost: missing pricing"),
	}

	q := Querier[*MockQuerier]{
		Raw:             true,
		out:             &strings.Builder{},
		shouldSaveReply: true,
		configDir:       tmpConfigDir,
		costManager:     costMgr,
		costMgrRdyChan:  ready,
		chat: pub_models.Chat{
			ID: "globalScope",
			Messages: []pub_models.Message{
				{Role: "system", Content: "sys"},
				{Role: "user", Content: "hello"},
				{Role: "system", Content: "answer"},
			},
			TokenUsage: &pub_models.Usage{
				PromptTokens:     1,
				CompletionTokens: 2,
				TotalTokens:      3,
			},
		},
	}

	q.postProcess()

	saved, err := chat.LoadPrevQuery(tmpConfigDir)
	if err != nil {
		t.Fatalf("load prev query: %v", err)
	}
	if saved.TokenUsage == nil {
		t.Fatalf("expected token usage to be saved")
	}
	if len(saved.Queries) != 0 {
		t.Fatalf("expected no saved queries when enrich fails, got %d", len(saved.Queries))
	}
}

func Test_Querier_Query_ToolCallRecursion_PreservesConversationMessages(t *testing.T) {
	tmpConfigDir := path.Join(t.TempDir(), ".clai")
	if err := os.MkdirAll(
		path.Join(tmpConfigDir, "conversations"),
		os.ModePerm,
	); err != nil {
		t.Fatalf("mkdir conversations: %v", err)
	}

	model := &MockQuerier{}
	callCount := 0
	model.streamFn = func(
		ctx context.Context,
		chat pub_models.Chat,
	) (chan models.CompletionEvent, error) {
		callCount++
		out := make(chan models.CompletionEvent, 4)
		go func() {
			defer close(out)
			if callCount == 1 {
				out <- pub_models.Call{
					ID:   "call1",
					Name: "pwd",
				}
				return
			}
			out <- "final answer"
		}()
		return out, nil
	}

	q := Querier[*MockQuerier]{
		Raw:             true,
		out:             &strings.Builder{},
		shouldSaveReply: true,
		configDir:       tmpConfigDir,
		costMgrRdyChan:  makeClosedChan(),
		chat: pub_models.Chat{
			ID: "globalScope",
			Messages: []pub_models.Message{
				{Role: "user", Content: "hello there"},
			},
		},
		Model: model,
	}

	if err := q.Query(context.Background()); err != nil {
		t.Fatalf("Query returned err: %v", err)
	}

	saved, err := chat.LoadPrevQuery(tmpConfigDir)
	if err != nil {
		t.Fatalf("load prev query: %v", err)
	}
	if len(saved.Messages) != 4 {
		t.Fatalf("expected 4 saved messages, got: %d", len(saved.Messages))
	}
	if saved.Messages[1].Role != "assistant" || len(saved.Messages[1].ToolCalls) != 1 {
		t.Fatalf("expected assistant tool call at message 1, got: %+v",
			saved.Messages[1])
	}
	if saved.Messages[2].Role != "tool" {
		t.Fatalf("expected tool result at message 2, got: %+v",
			saved.Messages[2])
	}
	if saved.Messages[3].Content != "final answer" {
		t.Fatalf("expected final answer, got: %q",
			saved.Messages[3].Content)
	}
}

func Test_Querier_Query_ToolCallRecursion_UsesFinalAssistantTurnTokenUsageForCostEnrichment(t *testing.T) {
	tmpConfigDir := path.Join(t.TempDir(), ".clai")
	if err := os.MkdirAll(
		path.Join(tmpConfigDir, "conversations"),
		os.ModePerm,
	); err != nil {
		t.Fatalf("mkdir conversations: %v", err)
	}

	var enrichCalls int
	costMgr := &mockCostManager{
		t: t,
		enrichFn: func(got pub_models.Chat) pub_models.Chat {
			enrichCalls++
			if got.TokenUsage == nil {
				t.Fatalf("expected token usage to be populated before enrich")
			}
			if got.TokenUsage.PromptTokens != 99 {
				t.Fatalf("prompt tokens mismatch: got %d want %d", got.TokenUsage.PromptTokens, 99)
			}
			if got.TokenUsage.CompletionTokens != 111 {
				t.Fatalf("completion tokens mismatch: got %d want %d", got.TokenUsage.CompletionTokens, 111)
			}
			if got.TokenUsage.TotalTokens != 210 {
				t.Fatalf("total tokens mismatch: got %d want %d", got.TokenUsage.TotalTokens, 210)
			}
			return got
		},
	}

	model := &MockQuerier{}
	callCount := 0
	model.streamFn = func(
		ctx context.Context,
		chat pub_models.Chat,
	) (chan models.CompletionEvent, error) {
		callCount++
		out := make(chan models.CompletionEvent, 4)
		go func() {
			defer close(out)
			if callCount == 1 {
				model.usage = &pub_models.Usage{
					PromptTokens:     2,
					CompletionTokens: 4,
					TotalTokens:      6,
				}
				out <- pub_models.Call{
					ID:   "call1",
					Name: "pwd",
				}
				return
			}
			model.usage = &pub_models.Usage{
				PromptTokens:     99,
				CompletionTokens: 111,
				TotalTokens:      210,
			}
			out <- "final answer"
		}()
		return out, nil
	}

	q := Querier[*MockQuerier]{
		Raw:             true,
		out:             &strings.Builder{},
		shouldSaveReply: true,
		configDir:       tmpConfigDir,
		costManager:     costMgr,
		costMgrRdyChan:  makeClosedChan(),
		chat: pub_models.Chat{
			ID: "globalScope",
			Messages: []pub_models.Message{
				{Role: "user", Content: "hello there"},
			},
		},
		Model: model,
	}

	if err := q.Query(context.Background()); err != nil {
		t.Fatalf("Query returned err: %v", err)
	}
	if enrichCalls != 1 {
		t.Fatalf("expected 1 enrich call, got: %d", enrichCalls)
	}
}

func Test_Querier_Query_ToolCallRecursion_AccumulatesNestedTokenUsageForCostEnrichment(t *testing.T) {
	tmpConfigDir := path.Join(t.TempDir(), ".clai")
	if err := os.MkdirAll(
		path.Join(tmpConfigDir, "conversations"),
		os.ModePerm,
	); err != nil {
		t.Fatalf("mkdir conversations: %v", err)
	}

	var enrichCalls int
	costMgr := &mockCostManager{
		t: t,
		enrichFn: func(got pub_models.Chat) pub_models.Chat {
			enrichCalls++
			if got.TokenUsage == nil {
				t.Fatalf("expected token usage to be populated before enrich")
			}
			if got.TokenUsage.PromptTokens != 99 {
				t.Fatalf("prompt tokens mismatch: got %d want %d", got.TokenUsage.PromptTokens, 99)
			}
			if got.TokenUsage.CompletionTokens != 111 {
				t.Fatalf("completion tokens mismatch: got %d want %d", got.TokenUsage.CompletionTokens, 111)
			}
			if got.TokenUsage.TotalTokens != 210 {
				t.Fatalf("total tokens mismatch: got %d want %d", got.TokenUsage.TotalTokens, 210)
			}
			return got
		},
	}

	model := &MockQuerier{}
	callCount := 0
	model.streamFn = func(
		ctx context.Context,
		chat pub_models.Chat,
	) (chan models.CompletionEvent, error) {
		callCount++
		out := make(chan models.CompletionEvent, 4)
		go func() {
			defer close(out)
			if callCount == 1 {
				model.usage = &pub_models.Usage{
					PromptTokens:     2,
					CompletionTokens: 4,
					TotalTokens:      6,
				}
				out <- pub_models.Call{
					ID:   "call1",
					Name: "pwd",
				}
				return
			}
			model.usage = &pub_models.Usage{
				PromptTokens:     99,
				CompletionTokens: 111,
				TotalTokens:      210,
			}
			out <- "final answer"
		}()
		return out, nil
	}

	q := Querier[*MockQuerier]{
		Raw:             true,
		out:             &strings.Builder{},
		shouldSaveReply: true,
		configDir:       tmpConfigDir,
		costManager:     costMgr,
		costMgrRdyChan:  makeClosedChan(),
		chat: pub_models.Chat{
			ID: "globalScope",
			Messages: []pub_models.Message{
				{Role: "user", Content: "hello there"},
			},
		},
		Model: model,
	}

	if err := q.Query(context.Background()); err != nil {
		t.Fatalf("Query returned err: %v", err)
	}
	if enrichCalls != 1 {
		t.Fatalf("expected 1 enrich call, got: %d", enrichCalls)
	}
}

func Test_Querier_Query_ToolCallRecursion_CostEnrichmentUsesFinalAssistantTurnTokenUsage(t *testing.T) {
	tmpConfigDir := path.Join(t.TempDir(), ".clai")
	if err := os.MkdirAll(
		path.Join(tmpConfigDir, "conversations"),
		os.ModePerm,
	); err != nil {
		t.Fatalf("mkdir conversations: %v", err)
	}

	var enrichCalls int
	costMgr := &mockCostManager{
		t: t,
		enrichFn: func(got pub_models.Chat) pub_models.Chat {
			enrichCalls++
			if got.TokenUsage == nil {
				t.Fatalf("expected token usage to be populated before enrich")
			}
			if got.TokenUsage.PromptTokens != 3 {
				t.Fatalf("prompt tokens mismatch: got %d want %d", got.TokenUsage.PromptTokens, 3)
			}
			if got.TokenUsage.CompletionTokens != 5 {
				t.Fatalf("completion tokens mismatch: got %d want %d", got.TokenUsage.CompletionTokens, 5)
			}
			if got.TokenUsage.TotalTokens != 8 {
				t.Fatalf("total tokens mismatch: got %d want %d", got.TokenUsage.TotalTokens, 8)
			}
			return got
		},
	}

	model := &MockQuerier{}
	callCount := 0
	model.streamFn = func(
		ctx context.Context,
		chat pub_models.Chat,
	) (chan models.CompletionEvent, error) {
		callCount++
		out := make(chan models.CompletionEvent, 4)
		go func() {
			defer close(out)
			if callCount == 1 {
				model.usage = &pub_models.Usage{
					PromptTokens:     2,
					CompletionTokens: 4,
					TotalTokens:      6,
				}
				out <- pub_models.Call{
					ID:   "call1",
					Name: "pwd",
				}
				return
			}
			model.usage = &pub_models.Usage{
				PromptTokens:     3,
				CompletionTokens: 5,
				TotalTokens:      8,
			}
			out <- "final answer"
		}()
		return out, nil
	}

	q := Querier[*MockQuerier]{
		Raw:             true,
		out:             &strings.Builder{},
		shouldSaveReply: true,
		configDir:       tmpConfigDir,
		costManager:     costMgr,
		costMgrRdyChan:  makeClosedChan(),
		chat: pub_models.Chat{
			ID: "globalScope",
			Messages: []pub_models.Message{
				{Role: "user", Content: "hello there"},
			},
		},
		Model: model,
	}

	if err := q.Query(context.Background()); err != nil {
		t.Fatalf("Query returned err: %v", err)
	}
	if enrichCalls != 1 {
		t.Fatalf("expected 1 enrich call, got: %d", enrichCalls)
	}
}

func makeClosedChan() <-chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

type mockCostManager struct {
	t         *testing.T
	calls     int
	enrichFn  func(pub_models.Chat) pub_models.Chat
	enrichErr error
}

func (m *mockCostManager) Start(
	context.Context,
) (<-chan struct{}, <-chan error) {
	ready := make(chan struct{})
	close(ready)
	errCh := make(chan error)
	return ready, errCh
}

func (m *mockCostManager) Enrich(chat pub_models.Chat) (
	pub_models.Chat,
	error,
) {
	m.calls++
	if m.enrichErr != nil {
		return pub_models.Chat{}, fmt.Errorf("mock enrich: %w", m.enrichErr)
	}
	if m.enrichFn == nil {
		return chat, nil
	}
	return m.enrichFn(chat), nil
}

type MockQuerierRateLimitTokenCountFail struct{}

func (q *MockQuerierRateLimitTokenCountFail) Setup() error { return nil }

func (q *MockQuerierRateLimitTokenCountFail) TokenUsage() *pub_models.Usage { return nil }

func (q *MockQuerierRateLimitTokenCountFail) CountInputTokens(
	context.Context,
	pub_models.Chat,
) (int, error) {
	return 0, fmt.Errorf("mock token count failure")
}

func (q *MockQuerierRateLimitTokenCountFail) StreamCompletions(
	context.Context,
	pub_models.Chat,
) (chan models.CompletionEvent, error) {
	return nil, &models.ErrRateLimit{
		ResetAt:         time.Now().Add(time.Millisecond),
		TokensRemaining: 0,
	}
}

func Test_ChatQuerier(t *testing.T) {
	q := Querier[*MockQuerier]{
		Model: &MockQuerier{
			shouldBlock: true,
		},
	}
	models.ChatQuerier_Test(t, &q)
}
