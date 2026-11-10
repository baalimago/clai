package setup

import (
	"context"
	"testing"

	"github.com/baalimago/clai/internal/testenv"
	"github.com/baalimago/clai/internal/testenv/procenv"
)

func TestMain(m *testing.M) {
	testenv.Main(m)
}

// procCtx is a run context over the current process streams.
func procCtx() context.Context {
	return procenv.Context(context.Background())
}
