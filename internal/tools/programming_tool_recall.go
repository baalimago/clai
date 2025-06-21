package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strconv"
)

// RecallTool fetches a message from a stored conversation
// given its name and message index.
type RecallTool Specification

// Recall is the exported instance of RecallTool.
var Recall = RecallTool{
	Name:        "recall",
	Description: "Recall a message from a stored conversation by name and index.",
	Inputs: &InputSchema{
		Type: "object",
		Properties: map[string]ParameterObject{
			"conversation": {
				Type:        "string",
				Description: "Conversation name or id",
			},
			"index": {
				Type:        "integer",
				Description: "Index of the message to retrieve",
			},
		},
		Required: &[]string{"conversation", "index"},
	},
}

func (r RecallTool) Call(input Input) (string, error) {
	convName, ok := input["conversation"].(string)
	if !ok {
		return "", fmt.Errorf("conversation must be a string")
	}

	var idx int
	switch v := input["index"].(type) {
	case int:
		idx = v
	case float64:
		idx = int(v)
	case string:
		n, err := strconv.Atoi(v)
		if err != nil {
			return "", fmt.Errorf("index must be a number")
		}
		idx = n
	default:
		return "", fmt.Errorf("index must be a number")
	}

	confDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user config dir: %w", err)
	}
	pathToConv := path.Join(confDir, ".clai", "conversations", fmt.Sprintf("%s.json", convName))
	b, err := os.ReadFile(pathToConv)
	if err != nil {
		return "", fmt.Errorf("failed to load conversation: %w", err)
	}

	var conv struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}

	if err := json.Unmarshal(b, &conv); err != nil {
		return "", fmt.Errorf("failed to decode conversation: %w", err)
	}

	if idx < 0 || idx >= len(conv.Messages) {
		return "", fmt.Errorf("index out of range")
	}
	msg := conv.Messages[idx]
	return fmt.Sprintf("%s: %s", msg.Role, msg.Content), nil
}

func (r RecallTool) Specification() Specification {
	return Specification(Recall)
}
