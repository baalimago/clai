package process

import (
	"os"
	"testing"

	"github.com/baalimago/clai/internal/term"
	"github.com/baalimago/clai/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }

func TestStdin(t *testing.T) {
	in, isTerminal := Stdin()
	if in != os.Stdin || isTerminal != term.IsTerminal(os.Stdin) {
		t.Fatalf("Stdin = %v, %v; want os.Stdin and its terminal state", in, isTerminal)
	}
}
