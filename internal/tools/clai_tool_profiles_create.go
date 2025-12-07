package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// ClaiCreateProfile - Create a new profile
var ClaiCreateProfile = &claiCreateProfileTool{}

type claiCreateProfileTool struct{}

func (t *claiCreateProfileTool) Specification() pub_models.Specification {
	return pub_models.Specification{
		Name:        "clai_profiles_create",
		Description: "Create a new profile. Profiles allow specialized AI configurations for specific tasks.",
		Inputs: &pub_models.InputSchema{
			Type:     "object",
			Required: []string{"name", "prompt", "model"},
			Properties: map[string]pub_models.ParameterObject{
				"name": {
					Type:        "string",
					Description: "Name of the profile (used as filename)",
				},
				"prompt": {
					Type:        "string",
					Description: "The system prompt for the profile",
				},
				"model": {
					Type:        "string",
					Description: "The model to use (optional)",
				},
				"use_tools": {
					Type:        "boolean",
					Description: "Whether to allow tool usage (default true)",
				},
				"tools": {
					Type:        "array",
					Description: "List of allowed tools",
					Items: &pub_models.ParameterObject{
						Type: "string",
					},
				},
			},
		},
	}
}

func (t *claiCreateProfileTool) Call(input pub_models.Input) (string, error) {
	name, ok := input["name"].(string)
	if !ok || name == "" {
		return "", fmt.Errorf("name is required and must be a string")
	}
	prompt, ok := input["prompt"].(string)
	if !ok || prompt == "" {
		return "", fmt.Errorf("prompt is required and must be a string")
	}

	model, _ := input["model"].(string)

	useTools := true
	if ut, ok := input["use_tools"].(bool); ok {
		useTools = ut
	}

	var tools []string
	if tRaw, ok := input["tools"]; ok {
		if tList, ok := tRaw.([]interface{}); ok {
			for _, item := range tList {
				if s, ok := item.(string); ok {
					tools = append(tools, s)
				}
			}
		} else if tList, ok := tRaw.([]string); ok {
			tools = tList
		}
	}

	profile := textProfile{
		Name:            name,
		Model:           model,
		UseTools:        useTools,
		Tools:           tools,
		Prompt:          prompt,
		SaveReplyAsConv: true, // Defaulting to true as per general preference
	}

	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user cache dir: %w", err)
	}

	profileDir := filepath.Join(cacheDir, "clai", "dynProfiles")
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create profile directory: %w", err)
	}

	filePath := filepath.Join(profileDir, fmt.Sprintf("%s.json", name))
	file, err := os.Create(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to create profile file: %w", err)
	}
	defer file.Close()

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")
	if err := enc.Encode(profile); err != nil {
		return "", fmt.Errorf("failed to encode profile: %w", err)
	}

	return fmt.Sprintf("Profile '%s' created successfully at %s", name, filePath), nil
}
