package text

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

type mockCompleter struct{}

func (m mockCompleter) Setup() error {
	return nil
}

func (m mockCompleter) StreamCompletions(ctx context.Context, c models.Chat) (chan models.CompletionEvent, error) {
	return nil, nil
}

func Test_executeAiCmd(t *testing.T) {
	testCases := []struct {
		description string
		setup       func(t *testing.T)
		given       string
		want        string
		wantErr     error
	}{
		{
			description: "it should run shell cmd",
			given:       "printf 'test'",
			want:        fmt.Sprintf(okFormat, "'test'"),
			wantErr:     nil,
		},
		{
			description: "it should work with quotes",
			setup: func(t *testing.T) {
				t.Helper()
				os.Chdir(filepath.Dir(testboil.CreateTestFile(t, "testfile").Name()))
			},
			given:   "find ./ -name \"testfile\"",
			want:    fmt.Sprintf(okFormat, "./testfile\n"),
			wantErr: nil,
		},
		{
			description: "it should work without quotes",
			setup: func(t *testing.T) {
				t.Helper()
				os.Chdir(filepath.Dir(testboil.CreateTestFile(t, "testfile").Name()))
			},
			given:   "find ./ -name testfile",
			want:    fmt.Sprintf(okFormat, "./testfile\n"),
			wantErr: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			q := Querier[mockCompleter]{}
			if tc.setup != nil {
				tc.setup(t)
			}
			q.fullMsg = tc.given
			gotFormated, gotErr := q.executeAiCmd()

			if gotFormated != tc.want {
				t.Fatalf("expected: %v, got: %v", tc.want, gotFormated)
			}

			if gotErr != tc.wantErr {
				t.Fatalf("expected error: %v, got: %v", tc.wantErr, gotErr)
			}
		})
	}
}
