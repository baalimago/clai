package audio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/baalimago/clai/internal/modelalias"
	"github.com/baalimago/clai/internal/modelfallback"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/remote"
	"github.com/baalimago/clai/internal/utils"
)

// setupTranscribeQuerier builds the transcription querier for the audio
// transcribe subcommand, with the configured model alias resolved to the
// ordered models a run tries (architecture/model-aliases.md).
func setupTranscribeQuerier(ctx context.Context, confDir string, f *Flags, args []string) (models.Querier, error) {
	return setupTranscribeQuerierWithResolver(ctx, confDir, f, args, nil)
}

func setupTranscribeQuerierWithResolver(ctx context.Context, confDir string, f *Flags, args []string, newResolver func(string, remote.Prompter) (remote.Resolver, error)) (models.Querier, error) {
	filePath, cleanup, err := resolveAudioInput(args)
	if err != nil {
		return nil, err
	}
	aConf, err := loadTranscribeConfig(confDir, f)
	if err != nil {
		cleanup()
		return nil, err
	}
	if err := resolveRemoteAudioModel(ctx, confDir, newResolver, &aConf); err != nil {
		cleanup()
		return nil, err
	}
	q, err := transcribeChain(ctx, aConf, filePath)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("failed to create audio querier: %w", err)
	}
	return &cleanupQuerier{Querier: q, cleanup: cleanup}, nil
}

func resolveRemoteAudioModel(ctx context.Context, confDir string, newResolver func(string, remote.Prompter) (remote.Resolver, error), aConf *Configurations) error {
	if !remote.IsRemote(aConf.Transcribe.Model) {
		return nil
	}
	models, err := remote.ResolveModelTarget(ctx, confDir, newResolver, aConf.Transcribe.Model)
	if err != nil {
		return err
	}
	aConf.ModelAliases = modelalias.Table{aConf.Transcribe.Model: models}
	return nil
}

func loadTranscribeConfig(confDir string, f *Flags) (Configurations, error) {
	aConf, err := utils.LoadConfigFromFile(confDir, "audioConfig.json", nil, &Default)
	if err != nil {
		return Configurations{}, fmt.Errorf("failed to load configs: %w", err)
	}
	if err := ApplyFlagOverrides(&aConf, f); err != nil {
		return Configurations{}, err
	}
	if _, err := ParseOutputFormat(aConf.Transcribe.OutputFormat); err != nil {
		return Configurations{}, err
	}
	if _, err := ResolveBudgets(aConf.Transcribe); err != nil {
		return Configurations{}, err
	}
	return aConf, nil
}

// transcribeChain composes one transcription run per candidate model. The
// transient input is not removed per attempt: a candidate that cannot be
// routed must leave the file for the next one
// (architecture/model-aliases.md).
func transcribeChain(ctx context.Context, aConf Configurations, filePath string) (models.Querier, error) {
	build := func(_ context.Context, model string) (models.Querier, error) {
		perModel := aConf
		perModel.Transcribe.Model = model
		return CreateQuerier(perModel, filePath)
	}
	return modelfallback.New(ctx, aConf.ModelAliases, aConf.Transcribe.Model, build, modelfallback.NoticeTo(os.Stderr))
}

// cleanupQuerier removes the transient input one run read after it ends, once,
// whatever model answered. It reports the completion-bell policy of the querier
// it wraps, so a transcript stays off the bell like a bare transcribe querier.
type cleanupQuerier struct {
	models.Querier
	cleanup func()
}

func (q *cleanupQuerier) Query(ctx context.Context) error {
	defer q.cleanup()
	return q.Querier.Query(ctx)
}

func (q *cleanupQuerier) Answer() models.Querier {
	return q.Querier
}

// SuppressCompletionNotification defaults to true: an audio run writes its
// transcript to stdout and nothing else.
func (q *cleanupQuerier) SuppressCompletionNotification() bool {
	if suppressor, ok := q.Querier.(interface{ SuppressCompletionNotification() bool }); ok {
		return suppressor.SuppressCompletionNotification()
	}
	return true
}

// resolveAudioInput returns the audio file path from the positional args.
// '-' drains stdin into a temp file; the cleanup removes it after the query.
func resolveAudioInput(args []string) (string, func(), error) {
	noop := func() {}
	if len(args) == 0 {
		return "", noop, fmt.Errorf("missing audio file argument, usage: clai audio transcribe <file>")
	}
	filePath := args[0]
	if filePath == "-" {
		// The extension is load-bearing downstream (vendor multipart filename,
		// ffmpeg chunk pattern), so sniff the container before creating the file
		header := make([]byte, SniffLen)
		n, err := io.ReadFull(os.Stdin, header)
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			return "", noop, fmt.Errorf("failed to read stdin audio: %w", err)
		}
		ext, err := DetectExtension(header[:n])
		if err != nil {
			return "", noop, fmt.Errorf("failed to detect stdin audio format: %w", err)
		}
		tmpFile, err := os.CreateTemp("", "clai-audio-stdin-*"+ext)
		if err != nil {
			return "", noop, fmt.Errorf("failed to create temp file for stdin audio: %w", err)
		}
		cleanup := func() { os.Remove(tmpFile.Name()) }
		_, err = tmpFile.Write(header[:n])
		if err == nil {
			_, err = io.Copy(tmpFile, os.Stdin)
		}
		if closeErr := tmpFile.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			cleanup()
			return "", noop, fmt.Errorf("failed to read stdin audio: %w", err)
		}
		return tmpFile.Name(), cleanup, nil
	}
	if _, err := os.Stat(filePath); err != nil {
		return "", noop, fmt.Errorf("failed to find audio file: %w", err)
	}
	return filePath, noop, nil
}
