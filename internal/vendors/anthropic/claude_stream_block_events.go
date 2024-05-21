package anthropic

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/tools"
)

func (c *Claude) handleContentBlockStart(blockStart string) models.CompletionEvent {
	var blockSuper ContentBlockSuper
	blockStart = trimDataPrefix(blockStart)
	if err := json.Unmarshal([]byte(blockStart), &blockSuper); err != nil {
		return fmt.Errorf("failed to unmarshal blockStart with content: %v, error: %w", blockStart, err)
	}
	block := blockSuper.ContentBlock
	c.contentBlockType = block.Type
	switch block.Type {
	case "tool_use":
		c.functionName = block.Name
	}
	return models.NoopEvent{}
}

func (c *Claude) handleContentBlockDelta(deltaToken string) models.CompletionEvent {
	delta, err := c.stringFromDeltaToken(deltaToken)
	if err != nil {
		return fmt.Errorf("failed to convert string to delta token: %w", err)
	}
	if c.debug {
		fmt.Printf("deltaToken: '%v', claudeMsg: '%v'", deltaToken, delta)
	}
	switch delta.Type {
	case "text_delta":
		if delta.Text == "" {
			return errors.New("unexpected empty response")
		}
		return delta.Text
	case "input_json_delta":
		return c.handleInputJsonDelta(delta)
	default:
		return fmt.Errorf("unexpected delta type: %v", delta.Type)
	}
}

func (c *Claude) handleInputJsonDelta(delta Delta) models.CompletionEvent {
	partial := delta.PartialJson
	c.functionJson += partial
	return partial
}

func (c *Claude) handleContentBlockStop(blockStop string) models.CompletionEvent {
	var block ContentBlock
	blockStop = trimDataPrefix(blockStop)
	if err := json.Unmarshal([]byte(blockStop), &block); err != nil {
		return fmt.Errorf("failed to unmarshal blockStop: %w", err)
	}

	switch c.contentBlockType {
	case "tool_use":
		var inputs tools.Input
		if err := json.Unmarshal([]byte(c.functionJson), &inputs); err != nil {
			return fmt.Errorf("failed to unmarshal functionJson: %v, error is: %w", c.functionJson, err)
		}
		return tools.Call{
			Name:   c.functionName,
			Inputs: inputs,
		}
	}
	return models.NoopEvent{}
}
