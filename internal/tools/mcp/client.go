package mcp

import (
	"context"

	"github.com/baalimago/clai/internal/tools"
)

func Client(ctx context.Context, mcpConfig tools.McpServer) (chan<- any, <-chan any, error) {
	inputChan := make(chan any)
	outputChan := make(chan any)

	go func() {
		<-ctx.Done()
		close(inputChan)
		close(outputChan)
	}()

	_ = mcpConfig // placeholder for future use

	return inputChan, outputChan, nil
}
