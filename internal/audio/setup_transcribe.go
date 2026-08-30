package audio

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/utils"
)

// setupTranscribeQuerier builds the transcription querier for the audio
// transcribe subcommand.
func setupTranscribeQuerier(confDir string, f *Flags, args []string) (models.Querier, error) {
	filePath, cleanup, err := resolveAudioInput(args)
	if err != nil {
		return nil, err
	}
	aConf, err := utils.LoadConfigFromFile(confDir, "audioConfig.json", nil, &Default)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("failed to load configs: %w", err)
	}
	ApplyFlagOverrides(&aConf, f)
	q, err := CreateQuerier(aConf, filePath, cleanup)
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
