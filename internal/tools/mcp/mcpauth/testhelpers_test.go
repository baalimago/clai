package mcpauth

import (
	"bytes"
	"net/url"
	"os"
	"strings"
	"sync"
)

// testEnvFileLoader is a minimal KEY=VALUE parser, standing in for
// mcp.LoadEnvFile: this package cannot import internal/tools/mcp (which
// imports this package for its own mcp/cmd.go subcommand), so production
// code takes the loader as an injected EnvFileLoader and the real parser's
// own correctness is covered by internal/tools/mcp's existing tests.
func testEnvFileLoader(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	env := make(map[string]string)
	for line := range strings.Lines(string(data)) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		env[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return env, nil
}

// pipeBuffer is an io.Reader/io.Writer a test can write a pasted line into
// before a blocking read starts, and read from afterwards. A plain
// bytes.Buffer is not safe for this since a concurrent Read/Write pair
// would race; this type serialises both behind one mutex.
type pipeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func newPipeBuffer() *pipeBuffer { return &pipeBuffer{} }

func (p *pipeBuffer) Read(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.buf.Read(b)
}

func (p *pipeBuffer) writeLine(s string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.buf.WriteString(s)
	p.buf.WriteByte('\n')
}

// extractQueryParam pulls name out of rawURL's query string, or "" when
// rawURL does not parse or the parameter is absent.
func extractQueryParam(rawURL, name string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Query().Get(name)
}
