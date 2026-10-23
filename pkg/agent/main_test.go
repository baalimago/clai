package agent

import (
	"fmt"
	"os"
	"testing"

	"github.com/baalimago/clai/internal/testenv"
)

func TestMain(m *testing.M) {
	// A developer's key would make every agent run fetch the live OpenRouter catalog.
	_ = os.Unsetenv("OPENROUTER_API_KEY")
	home, err := testenv.IsolateHome()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(string(home))
	os.Exit(code)
}
