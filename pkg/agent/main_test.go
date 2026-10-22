package agent

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	// A developer's key would make every agent run fetch the live OpenRouter catalog.
	_ = os.Unsetenv("OPENROUTER_API_KEY")
	os.Exit(m.Run())
}
