package testserverbin

import (
	"os"
	"sync"
	"testing"

	"github.com/baalimago/clai/internal/testenv"
)

// One build serves every -count iteration; TestMain removes it.
var server struct {
	once sync.Once
	path string
	err  error
}

func builtServer() (string, error) {
	server.once.Do(func() { server.path, server.err = Build() })
	return server.path, server.err
}

func TestMain(m *testing.M) {
	testenv.LimitProcs()
	code := m.Run()
	if server.path != "" {
		_ = os.Remove(server.path)
	}
	os.Exit(code)
}
