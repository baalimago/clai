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

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/reply"
	"github.com/baalimago/clai/internal/tools"
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

func (q *MockQuerier) StreamCompletions(ctx context.Context, chat models.Chat) (chan models.CompletionEvent, error) {
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
		q, err := NewQuerier(conf, &MockQuerier{})
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
			desc: "it should print token to stdout",
			q: Querier[*MockQuerier]{
				Raw: true,
				Model: &MockQuerier{
					completionChan: make(chan models.CompletionEvent),
				},
			},
			given: []models.CompletionEvent{"test", "CLOSE"},
			want:  "test",
		},
		{
			desc: "token whitespace should be respected",
			q: Querier[*MockQuerier]{
				Raw: true,
				Model: &MockQuerier{
					completionChan: make(chan models.CompletionEvent),
				},
			},
			given: []models.CompletionEvent{" one", "two\n", "  three ", "CLOSE"},
			want: ` onetwo
  three `,
		},
		{
			desc: "it should call test function when asked to",
			q: Querier[*MockQuerier]{
				Raw: true,
				Model: &MockQuerier{
					completionChan: make(chan models.CompletionEvent),
				},
			},
			given: []models.CompletionEvent{
				"first the model said something",
				tools.Call{
					Name: "test",
					Inputs: tools.Input{
						"testKey": "testVal",
					},
				},
				"CLOSE",
			},
			want: "first the model said somethingretrieved tool_calls struct from AI:\n{\"name\":\"test\",\"inputs\":{\"testKey\":\"testVal\"}}\n{Name:test Inputs:map[testKey:testVal]}\n",
		},
	}
	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			go func() {
				for _, msg := range tC.given {
					tC.q.Model.completionChan <- msg
				}
			}()

			got := testboil.CaptureStdout(t, func(t *testing.T) {
				t.Helper()
				tC.q.Query(context.Background())
			})

			if got != tC.want {
				t.Fatalf("expected: %q, got: %q", tC.want, got)
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
	t.Run("it should only save to prevQuery if in reply mode", func(t *testing.T) {
		tmpConfigDir := path.Join(t.TempDir(), ".clai")
		os.MkdirAll(path.Join(tmpConfigDir, "conversations"), os.ModePerm)
		q := Querier[*MockQuerier]{
			Raw:             true,
			shouldSaveReply: true,
			configDir:       tmpConfigDir,
			chat: models.Chat{
				ID: "prevQuery",
				Messages: []models.Message{
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
		lastReply, err := reply.Load(q.configDir)
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
		lastReply, err = reply.Load(q.configDir)
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

func Test_ChatQuerier(t *testing.T) {
	q := &Querier[*MockQuerier]{
		Model: &MockQuerier{},
	}
	models.ChatQuerier_Test(t, q)
}
