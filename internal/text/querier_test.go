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
	"time"

	"testing"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

type MockQuerier struct {
	Somefield   string `json:"somefield"`
	shouldBlock bool
	// stringChan is used to simulate a stream of completions
	// send 'CLOSE' outChan, used in tests, plus the MockQuerier
	stringChan chan string
	// errChan is used to simulate a stream of errors. Send
	errChan chan error
}

func (q *MockQuerier) Setup() error {
	return nil
}

func (q *MockQuerier) StreamCompletions(ctx context.Context, chat models.Chat) (chan models.CompletionEvent, error) {
	if q.shouldBlock {
		ch := make(chan struct{})
		<-ch
	}
	// simulate a stream of completions via the sendChan, so that
	// it's possible to send messages from outside the test
	if q.stringChan != nil {
		outChan := make(chan models.CompletionEvent)
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case err := <-q.errChan:
					outChan <- models.CompletionEvent(err)
				case msg := <-q.stringChan:
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
		os.Mkdir(path.Join(tmpDir, ".clai"), os.FileMode(0755))
		err = os.WriteFile(path.Join(tmpDir, ".clai", fmt.Sprintf("%v.json", model)), bytes, os.FileMode(0755))
		if err != nil {
			t.Fatalf("failed to write mock savedModel: %v", err)
		}
		conf := Configurations{
			Model:     model,
			ConfigDir: tmpDir,
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
	}, time.Second*1)
}

func Test_Querier_Query_strings(t *testing.T) {
	testCases := []struct {
		desc  string
		q     Querier[*MockQuerier]
		given []string
		want  string
	}{
		{
			desc: "it should print to stdout",
			q: Querier[*MockQuerier]{
				Raw: true,
				Model: &MockQuerier{
					stringChan: make(chan string),
				},
			},
			given: []string{"test", "CLOSE"},
			want:  "test",
		},
		{
			desc: "token whitespace should be respected",
			q: Querier[*MockQuerier]{
				Raw: true,
				Model: &MockQuerier{
					stringChan: make(chan string),
				},
			},
			given: []string{" one", "two\n", "three ", "CLOSE"},
			want: ` onetwo
three `,
		},
	}
	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			go func() {
				for _, msg := range tC.given {
					tC.q.Model.stringChan <- msg
				}
			}()

			got := testboil.CaptureStdout(t, func(t *testing.T) {
				t.Helper()
				tC.q.Query(context.Background())
			})

			if got != tC.want {
				t.Fatalf("expected: %v, got: %v", tC.want, got)
			}
		})
	}
}

func Test_Querier_Query_errors(t *testing.T) {
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
					stringChan: make(chan string),
					errChan:    make(chan error),
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
					stringChan: make(chan string),
					errChan:    make(chan error),
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
					stringChan: make(chan string),
					errChan:    make(chan error),
				},
			},
			given: errors.New("some other error"),
			want:  errors.New("some other error"),
		},
	}
	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			go func() {
				tC.q.Model.errChan <- tC.given
				tC.q.Model.stringChan <- "CLOSE"
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

func Test_ChatQuerier(t *testing.T) {
	q := &Querier[*MockQuerier]{
		Model: &MockQuerier{},
	}
	models.ChatQuerier_Test(t, q)
}
