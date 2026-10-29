package audio

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/modelalias"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

// seedAudioAliases writes audioConfig.json naming model and the alias table,
// and returns the config dir.
func seedAudioAliases(t *testing.T, model string, table modelalias.Table) string {
	t.Helper()
	confDir := t.TempDir()
	conf := Default
	conf.Transcribe.Model = model
	conf.Transcribe.OutputFormat = string(FormatText)
	conf.ModelAliases = table
	if err := utils.CreateFile(filepath.Join(confDir, "audioConfig.json"), &conf); err != nil {
		t.Fatalf("CreateFile(audioConfig.json): %v", err)
	}
	return confDir
}

// transcribe runs one transcribe command setup and query, returning what the
// command wrote and the querier it composed.
func transcribe(t *testing.T, confDir string, args []string) (stdout, stderr string, q models.Querier, err error) {
	t.Helper()
	stdout = testboil.CaptureStdout(t, func(t *testing.T) {
		stderr = testboil.CaptureStderr(t, func(t *testing.T) {
			q, err = setupTranscribeQuerier(context.Background(), confDir, &Flags{}, args)
			if err != nil {
				return
			}
			err = q.Query(context.Background())
		})
	})
	return stdout, stderr, q, err
}

// transientAudioFiles lists the temp files a '-' run creates.
func transientAudioFiles(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(os.TempDir(), "clai-audio-stdin-*"))
	if err != nil {
		t.Fatalf("Glob(temp audio files): %v", err)
	}
	return paths
}

// TestSetupTranscribeQuerier_FallsBackToTheNextModel pins the cross-vendor
// ladder: a model this machine cannot route is replaced by the next candidate,
// and the fallback is a notice on stderr (architecture/model-aliases.md).
func TestSetupTranscribeQuerier_FallsBackToTheNextModel(t *testing.T) {
	confDir := seedAudioAliases(t, "fast", modelalias.Table{"fast": {"no-such-model", "test"}})

	stdout, stderr, _, err := transcribe(t, confDir, []string{sparseFile(t, 8)})
	if err != nil {
		t.Fatalf("transcribe: %v (stderr %q)", err, stderr)
	}
	if !strings.Contains(stdout, "mock transcription") {
		t.Fatalf("stdout = %q, want the transcript of the model that answered", stdout)
	}
	for _, want := range []string{"▸ model no-such-model unavailable · ", "trying test"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr = %q, want it to contain %q", stderr, want)
		}
	}
}

// TestSetupTranscribeQuerier_ExhaustedLadderNamesEveryModel pins that a ladder
// whose every candidate fails reports each model, so the user can fix it.
func TestSetupTranscribeQuerier_ExhaustedLadderNamesEveryModel(t *testing.T) {
	confDir := seedAudioAliases(t, "fast", modelalias.Table{
		"fast": {"no-such-model", "another-unknown-model"},
	})

	stdout, stderr, _, err := transcribe(t, confDir, []string{sparseFile(t, 8)})
	if err == nil {
		t.Fatalf("transcribe: no error, want every candidate reported (stdout %q)", stdout)
	}
	for _, want := range []string{"no-such-model", "another-unknown-model"} {
		if !strings.Contains(err.Error(), want) && !strings.Contains(stderr, want) {
			t.Fatalf("err = %v, stderr = %q, want both to name %q", err, stderr, want)
		}
	}
}

// TestSetupTranscribeQuerier_RemovesTransientInput pins that the temp file a
// '-' run creates is removed once, after whichever model answered, and also
// when the whole ladder fails.
func TestSetupTranscribeQuerier_RemovesTransientInput(t *testing.T) {
	t.Run("after the model that answered", func(t *testing.T) {
		confDir := seedAudioAliases(t, "fast", modelalias.Table{"fast": {"no-such-model", "test"}})
		setStdin(t, fakeWAVBytes("stdin-audio-for-fallback"))
		before := transientAudioFiles(t)

		_, stderr, _, err := transcribe(t, confDir, []string{"-"})
		if err != nil {
			t.Fatalf("transcribe: %v (stderr %q)", err, stderr)
		}
		if after := transientAudioFiles(t); len(after) != len(before) {
			t.Fatalf("temp audio files after the run = %v, want none new (before %v)", after, before)
		}
	})

	t.Run("after the ladder is exhausted", func(t *testing.T) {
		confDir := seedAudioAliases(t, "fast", modelalias.Table{"fast": {"no-such-model", "another-unknown-model"}})
		setStdin(t, fakeWAVBytes("stdin-audio-for-exhaustion"))
		before := transientAudioFiles(t)

		if _, _, _, err := transcribe(t, confDir, []string{"-"}); err == nil {
			t.Fatal("transcribe: no error, want the exhausted ladder")
		}
		if after := transientAudioFiles(t); len(after) != len(before) {
			t.Fatalf("temp audio files after the run = %v, want none new (before %v)", after, before)
		}
	})
}

// TestSetupTranscribeQuerier_SuppressesCompletionBell pins that a fallback
// chain keeps the bell off the transcript, as a plain transcript querier does.
func TestSetupTranscribeQuerier_SuppressesCompletionBell(t *testing.T) {
	for _, tc := range []struct {
		name  string
		model string
		table modelalias.Table
	}{
		{"plain model", "test", nil},
		{"alias", "fast", modelalias.Table{"fast": {"test", "whisper-1"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			confDir := seedAudioAliases(t, tc.model, tc.table)
			q, err := setupTranscribeQuerier(context.Background(), confDir, &Flags{}, []string{sparseFile(t, 8)})
			if err != nil {
				t.Fatalf("setupTranscribeQuerier: %v", err)
			}
			suppressor, ok := q.(interface{ SuppressCompletionNotification() bool })
			if !ok {
				t.Fatalf("querier %T does not suppress the completion bell", q)
			}
			if !suppressor.SuppressCompletionNotification() {
				t.Fatal("SuppressCompletionNotification() = false, want true")
			}
		})
	}
}

// TestSetupTranscribeQuerier_UnresolvableModelFailsBeforeAnyWork pins that a
// broken ladder fails the command and leaves no temp file behind.
func TestSetupTranscribeQuerier_UnresolvableModelFailsBeforeAnyWork(t *testing.T) {
	confDir := seedAudioAliases(t, "fast", modelalias.Table{"fast": {}})
	setStdin(t, fakeWAVBytes("stdin-audio-for-a-broken-ladder"))
	before := transientAudioFiles(t)

	_, _, _, err := transcribe(t, confDir, []string{"-"})
	if err == nil {
		t.Fatal("transcribe: no error, want the model alias error")
	}
	if !strings.Contains(err.Error(), "model alias") {
		t.Fatalf("err = %v, want it to name the model alias", err)
	}
	if after := transientAudioFiles(t); len(after) != len(before) {
		t.Fatalf("temp audio files after the run = %v, want none new (before %v)", after, before)
	}
}

// TestSetupTranscribeQuerier_InvalidConfigFailsWithoutFallback pins that a
// non-model configuration error (such as an invalid output format) fails before
// the alias ladder runs and prints no model-unavailable notice.
func TestSetupTranscribeQuerier_InvalidConfigFailsWithoutFallback(t *testing.T) {
	confDir := seedAudioAliases(t, "fast", modelalias.Table{"fast": {"test", "whisper-1"}})
	var f Flags
	if err := f.Format.Set("yaml"); err != nil {
		t.Fatalf("Format.Set: %v", err)
	}

	var stderr string
	var err error
	_ = testboil.CaptureStdout(t, func(t *testing.T) {
		stderr = testboil.CaptureStderr(t, func(t *testing.T) {
			var q models.Querier
			q, err = setupTranscribeQuerier(context.Background(), confDir, &f, []string{sparseFile(t, 8)})
			if err == nil {
				err = q.Query(context.Background())
			}
		})
	})
	if err == nil {
		t.Fatal("transcribe: no error, want the invalid output format error")
	}
	if strings.Contains(stderr, "▸ model ") {
		t.Fatalf("stderr = %q, want no model fallback notice for an invalid output format", stderr)
	}
}

// TestCleanupQuerier pins the removal contract: the transient input is removed
// after the query, whatever it returned.
func TestCleanupQuerier(t *testing.T) {
	t.Run("runs after the query", func(t *testing.T) {
		cleaned := false
		q := &cleanupQuerier{
			Querier: &TranscribeQuerier{Splitter: NewSplitter(MockTranscriber{}, &fakeRunner{}), FilePath: sparseFile(t, 8), Format: FormatText, Out: &strings.Builder{}},
			cleanup: func() { cleaned = true },
		}
		if err := q.Query(context.Background()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !cleaned {
			t.Error("expected cleanup to run")
		}
	})

	t.Run("runs after a failure", func(t *testing.T) {
		cleaned := false
		q := &cleanupQuerier{
			Querier: &TranscribeQuerier{FilePath: filepath.Join(t.TempDir(), "gone.wav"), Format: FormatText},
			cleanup: func() { cleaned = true },
		}
		if err := q.Query(context.Background()); err == nil {
			t.Fatal("expected error, got nil")
		}
		if !cleaned {
			t.Error("expected cleanup to run on error")
		}
	})
}
