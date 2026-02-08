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
	"unicode/utf8"

	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/models"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

type MockQuerier struct {
	Somefield   string `json:"somefield"`
	shouldBlock bool
	// completionChan is used to simulate a stream of completions
	// send 'CLOSE' outChan, used in tests, plus the MockQuerier
	completionChan chan models.CompletionEvent
	// errChan is used to simulate a stream of errors. Send
	errChan chan error
}

func (q *MockQuerier) Setup() error {
	return nil
}

func (q *MockQuerier) StreamCompletions(ctx context.Context, chat pub_models.Chat) (chan models.CompletionEvent, error) {
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
}

type MockQuerierRateLimitTokenCountFail struct{}

func (m *MockQuerierRateLimitTokenCountFail) Setup() error { return nil }

func (m *MockQuerierRateLimitTokenCountFail) StreamCompletions(context.Context, pub_models.Chat) (chan models.CompletionEvent, error) {
	return nil, models.NewRateLimitError(time.Now().Add(time.Millisecond), 1000, 0)
}

func (m *MockQuerierRateLimitTokenCountFail) CountInputTokens(context.Context, pub_models.Chat) (int, error) {
	return 0, errors.New("token count request failed")
}

func Test_ChatQuerier(t *testing.T) {
	q := &Querier[*MockQuerier]{
		Model: &MockQuerier{},
	}
	models.ChatQuerier_Test(t, q)
}

func Test_limitToolOutput(t *testing.T) {
	t.Run("should append disclaimer when exceeding limit", func(t *testing.T) {
		given := "abcdefghijklmnopqrstuvwxyz"
		got := limitToolOutput(given, 10)
		if !strings.Contains(got, "The tool's output has been restricted as it's too long") {
			t.Fatalf("expected disclaimer in output, got: %v", got)
		}
		runeLen := utf8.RuneCountInString(got)
		if runeLen <= 10 {
			t.Fatalf("expected output to be longer than limit due to disclaimer, got %v runes", runeLen)
		}
	})

	t.Run("should return same string when within limit", func(t *testing.T) {
		given := "short"
		got := limitToolOutput(given, 10)
		if got != given {
			t.Fatalf("expected %v, got %v", given, got)
		}
	})

	t.Run("should not limit output when limit is 0", func(t *testing.T) {
		given := "this is a very long string that would normally be limited"
		got := limitToolOutput(given, 0)
		if got != given {
			t.Fatalf("expected %v, got %v", given, got)
		}
	})
}
