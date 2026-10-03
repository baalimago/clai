package mcp

import (
	"fmt"
	"io"
	"os"
)

// stdioPipes holds both ends of the three pipes a server's stdio is wired to.
// clai owns them, because Wait closes a pipe exec created itself.
type stdioPipes struct {
	stdinR, stdinW   *os.File
	stdoutR, stdoutW *os.File
	stderrR, stderrW *os.File
}

func openStdioPipes() (*stdioPipes, error) {
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		closeFiles(stdinR, stdinW)
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		closeFiles(stdinR, stdinW, stdoutR, stdoutW)
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}
	return &stdioPipes{stdinR, stdinW, stdoutR, stdoutW, stderrR, stderrW}, nil
}

// closeChildEnds drops the parent's copies, so a reader's EOF means every
// process holding that pipe is gone.
func (p *stdioPipes) closeChildEnds() { closeFiles(p.stdinR, p.stdoutW, p.stderrW) }

func (p *stdioPipes) closeAll() {
	closeFiles(p.stdinR, p.stdinW, p.stdoutR, p.stdoutW, p.stderrR, p.stderrW)
}

func closeFiles(files ...io.Closer) {
	for _, f := range files {
		_ = f.Close()
	}
}

// closeReader closes a reader clai owns; a harness reader that is not a
// closer is left alone.
func closeReader(r io.Reader) {
	if closer, ok := r.(io.Closer); ok {
		_ = closer.Close()
	}
}
