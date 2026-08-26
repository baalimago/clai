package internal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/baalimago/clai/internal/audio"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/table"
)

const audioNamespaceHelp = `usage: clai [flags] audio <verb> [args]

Verbs:
  t|transcribe <file>   Transcribe an audio file to text. Use '-' to read audio bytes from stdin.
  h|help                Show this help.

Flags:
  -am, -audio-model     Set the transcription model (default in audioConfig.json)
  -af, -audio-format    Set the transcript output format: vtt|srt|text|json
  -parallelism          Max parallel requests when a large file is split into chunks

Examples:
  clai audio transcribe meeting.wav
  clai -af text a t meeting.wav
  cat meeting.wav | clai a t -
`

// handleAudio dispatches the audio namespace verbs; args[0] is "audio" or "a".
func handleAudio(_ context.Context, confDir string, flagSet Configurations, args []string) (models.Querier, error) {
	if len(args) < 2 {
		fmt.Fprint(os.Stderr, audioNamespaceHelp)
		return nil, fmt.Errorf("missing audio verb")
	}
	switch verb := args[1]; verb {
	case "transcribe", "t":
		return setupAudioTranscribeQuerier(confDir, flagSet, args[2:])
	case "help", "h":
		fmt.Print(audioNamespaceHelp)
		return nil, table.ErrUserInitiatedExit
	default:
		fmt.Fprint(os.Stderr, audioNamespaceHelp)
		return nil, fmt.Errorf("unknown audio verb: %q", verb)
	}
}

func setupAudioTranscribeQuerier(confDir string, flagSet Configurations, args []string) (models.Querier, error) {
	filePath, cleanup, err := resolveAudioInput(args)
	if err != nil {
		return nil, err
	}
	aConf, err := utils.LoadConfigFromFile(confDir, "audioConfig.json", nil, &audio.Default)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("failed to load configs: %w", err)
	}
	applyFlagOverridesForAudio(&aConf, flagSet, defaultFlags)
	q, err := CreateAudioQuerier(aConf, filePath, cleanup)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("failed to create audio querier: %w", err)
	}
	return q, nil
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
		header := make([]byte, audio.SniffLen)
		n, err := io.ReadFull(os.Stdin, header)
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			return "", noop, fmt.Errorf("failed to read stdin audio: %w", err)
		}
		ext, err := audio.DetectExtension(header[:n])
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
