package mcp

import (
	"context"

	"github.com/baalimago/clai/internal/tools"
)

func Client(ctx context.Context, mcpConfig tools.McpServer) (chan<- any, <-chan any, error) {
	// Implementation needed
	inputChan := make(chan any)
	outputChan := make(chan any)
	return inputChan, outputChan, nil
}
