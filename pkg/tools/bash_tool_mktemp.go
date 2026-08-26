package tools

import (
	"context"
	"fmt"
	"os"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type MktempTool pub_models.Specification

var Mktemp = MktempTool{
	Name:        "mktemp",
	Description: "Create a temporary directory that is removed when this clai run ends.",
	Inputs: &pub_models.InputSchema{
		Type:       "object",
		Required:   []string{},
		Properties: map[string]pub_models.ParameterObject{},
	},
}

func (m MktempTool) Call(pub_models.Input) (string, error) {
	return "", fmt.Errorf("mktemp requires a context-aware invocation")
}

// CallWithContext creates a temporary directory which is removed once ctx is
// canceled. The tool owns its own cleanup, so CLI runs and pkg agents behave
// identically as long as the caller cancels the context it queries with.
func (m MktempTool) CallWithContext(ctx context.Context, _ pub_models.Input) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("mktemp requires a context")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if ctx.Done() == nil {
		return "", fmt.Errorf("mktemp requires a cancellable context")
	}

	directory, err := os.MkdirTemp("", "clai-*")
	if err != nil {
		return "", fmt.Errorf("create temporary directory: %w", err)
	}
	context.AfterFunc(ctx, func() {
		_ = os.RemoveAll(directory)
	})
	return directory, nil
}

func (m MktempTool) Specification() pub_models.Specification {
	return pub_models.Specification(Mktemp)
}
