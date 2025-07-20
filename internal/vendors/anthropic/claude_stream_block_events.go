package anthropic

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/baalimago/clai/internal/models"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/debug"
)

func (c *Claude) handleContentBlockStart(blockStart string) models.CompletionEvent {
	var blockSuper ContentBlockSuper
	blockStart = trimDataPrefix(blockStart)
	if err := json.Unmarshal([]byte(blockStart), &blockSuper); err != nil {
		return fmt.Errorf("failed to unmarshal blockStart with content: %v, error: %w", blockStart, err)
	}
	block := blockSuper.ToolContentBlock
	c.contentBlockType = block.Type
	switch block.Type {
	case "tool_use":
		c.functionName = block.Name
		c.functionID = block.ID
	}
	return models.NoopEvent{}
}

// handleContentBlockDelta processes a delta token to generate a CompletionEvent.
// It converts the delta token into a structured format and evaluates the type of
// delta to determine the appropriate action. The function handles "text_delta"
// types by checking if the text content is empty, and returns an error if so.
// For "input_json_delta" types, it delegates processing to handleInputJSONDelta.
// Returns an error for unexpected delta types. JSON data is printed if debugging
// is enabled.
//
// Parameters:
// - deltaToken: A string representing the delta token to be processed.
//
// Returns:
// - models.CompletionEvent: A response event generated from the delta token.
// - error: An error is returned if the delta type is unexpected or the text is empty.
func (c *Claude) handleContentBlockDelta(deltaToken string) models.CompletionEvent {
	delta, err := c.stringFromDeltaToken(deltaToken)
	if err != nil {
		return fmt.Errorf("failed to convert string to delta token: %w", err)
	}
	if c.debug {
		fmt.Printf("deltaStruct: '%v'\n---\n",
			debug.IndentedJsonFmt(delta))
	}
	switch delta.Type {
	case "text_delta":
		if delta.Text == "" {
			return errors.New("unexpected empty response")
		}
		return delta.Text
	case "input_json_delta":
		return c.handleInputJSONDelta(delta)
	default:
		return fmt.Errorf("unexpected delta type: %v", delta.Type)
	}
}

func (c *Claude) handleInputJSONDelta(delta Delta) models.CompletionEvent {
	partial := delta.PartialJSON
	c.functionJSON += partial
	return partial
}

func (c *Claude) handleContentBlockStop(blockStop string) models.CompletionEvent {
	defer func() {
		c.debugFullStreamMsg = ""
		c.functionJSON = ""
	}()
	var block ToolUseContentBlock
	blockStop = trimDataPrefix(blockStop)
	if err := json.Unmarshal([]byte(blockStop), &block); err != nil {
		return fmt.Errorf("failed to unmarshal blockStop: %w", err)
	}

	switch c.contentBlockType {
	case "tool_use":
		var inputs pub_models.Input
		if c.functionJSON != "" {
			if err := json.Unmarshal([]byte(c.functionJSON), &inputs); err != nil {
				return fmt.Errorf("failed to unmarshal functionJSON: %v, error is: %w", c.functionJSON, err)
			}
		}
		return pub_models.Call{
			Name:   c.functionName,
			Inputs: &inputs,
			ID:     c.functionID,
		}
	}
	return models.NoopEvent{}
}
