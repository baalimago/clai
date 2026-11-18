package chat

import (
	"fmt"
	"io"
	"strings"

	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/utils"
)

// listScreen passes a table render to out and keeps its text, with the lines
// the terminal echoed as they were typed, so a redraw at a new width can
// erase it first.
type listScreen struct {
	out  io.Writer
	text strings.Builder
}

func (s *listScreen) Write(p []byte) (int, error) {
	n, err := s.out.Write(p)
	s.text.Write(p[:n])
	return n, err
}

// echoing reads in and keeps what it hands on as the terminal's echo.
func (s *listScreen) echoing(in io.Reader) io.Reader {
	return echoReader{in: in, screen: s}
}

type echoReader struct {
	in     io.Reader
	screen *listScreen
}

func (e echoReader) Read(p []byte) (int, error) {
	n, err := e.in.Read(p)
	e.screen.text.Write(p[:n])
	return n, err
}

func (s *listScreen) reset() { s.text.Reset() }

// erase moves to the first row the render takes once the terminal rewraps
// it width cells wide, and clears the screen from there down. Rows scrolled
// above the screen stay in the scrollback.
func (s *listScreen) erase(width int) error {
	up := utils.TerminalRows(s.text.String(), width) - 1
	s.text.Reset()
	seq := "\r\x1b[J"
	if up > 0 {
		seq = fmt.Sprintf("\r\x1b[%dA\x1b[J", up)
	}
	if _, err := io.WriteString(s.out, seq); err != nil {
		return diag.Errorf("erase the chat list: %w", err)
	}
	return nil
}
