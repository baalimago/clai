package apikey

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/testenv"
	"github.com/baalimago/clai/pkg/diagnostic"
)

func TestMain(m *testing.M) { testenv.Main(m) }

// TestMissingError pins the vendor's own text and a remedy that names the
// variable, reachable through wrapping, and never a key.
func TestMissingError(t *testing.T) {
	missing := &MissingError{Env: "OPENAI_API_KEY", Message: "openai: missing OPENAI_API_KEY"}
	wrapped := fmt.Errorf("failed to setup model: %w", error(missing))
	if wrapped.Error() != "failed to setup model: openai: missing OPENAI_API_KEY" {
		t.Fatalf("text = %q", wrapped.Error())
	}
	var remedier diagnostic.Remedier
	if !errors.As(wrapped, &remedier) {
		t.Fatal("the wrapped error carries no Remedier")
	}
	remedies := remedier.Remedies()
	if len(remedies) != 1 || remedies[0].Instruction != "Set OPENAI_API_KEY to your API key" || strings.Join(remedies[0].Command, " ") != "export OPENAI_API_KEY=<your key>" {
		t.Fatalf("remedies = %+v", remedies)
	}
}
