package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// textProfile narrowly dodges any cyclical import
type textProfile struct {
	Name            string   `json:"name"`
	Model           string   `json:"model"`
	UseTools        bool     `json:"use_tools"`
	Tools           []string `json:"tools"`
	Prompt          string   `json:"prompt"`
	SaveReplyAsConv bool     `json:"save-reply-as-conv"`
}

const profilePrintTemplate = `Name: %v
Model: %v
Tools: %v
First sentence prompt: %v
---
`

// ClaiListProfiles - List profiles
var ClaiListProfiles = &claiListProfilesTool{
	profilePrintTemplate,
}

type claiListProfilesTool struct {
	printTemplate string
}

func (t *claiListProfilesTool) Specification() pub_models.Specification {
	return pub_models.Specification{
		Name:        "clai_profiles_list",
		Description: "List all dynamic profiles. parsing them to show name, summary and path.",
		Inputs: &pub_models.InputSchema{
			Type:       "object",
			Properties: map[string]pub_models.ParameterObject{},
			Required:   make([]string, 0),
		},
	}
}

func loadDynProfiles(cacheDir string) ([]textProfile, error) {
	profileDir := filepath.Join(cacheDir, "clai", "dynProfiles")
	if _, err := os.Stat(profileDir); os.IsNotExist(err) {
		return nil, nil
	}

	files, err := os.ReadDir(profileDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory: %w", err)
	}

	var profiles []textProfile
	for _, f := range files {
		if f.IsDir() || filepath.Ext(f.Name()) != ".json" {
			continue
		}

		fullPath := filepath.Join(profileDir, f.Name())
		content, err := os.ReadFile(fullPath)
		if err != nil {
			continue
		}

		var prof textProfile
		if err := json.Unmarshal(content, &prof); err != nil {
			continue
		}

		profiles = append(profiles, prof)
	}

	return profiles, nil
}

func (t *claiListProfilesTool) Call(input pub_models.Input) (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user cache dir: %w", err)
	}

	profiles, err := loadDynProfiles(cacheDir)
	if err != nil {
		return "", fmt.Errorf("failed to get profiles: %w", err)
	}

	var builder strings.Builder
	for _, p := range profiles {
		builder.WriteString(fmt.Sprintf(t.printTemplate,
			p.Name,
			p.Model,
			p.Tools,
			getFirstSentence(p.Prompt)))
	}
	return builder.String(), nil
}

func getFirstSentence(s string) string {
	if s == "" {
		return "[Empty prompt]"
	}

	// Find first literal punctuation mark
	idxDot := strings.Index(s, ".")
	idxExcl := strings.Index(s, "!")
	idxQues := strings.Index(s, "?")
	idxNewLine := strings.Index(s, "\n")

	minIdx := len(s)

	if idxDot != -1 && idxDot < minIdx {
		minIdx = idxDot
	}
	if idxExcl != -1 && idxExcl < minIdx {
		minIdx = idxExcl
	}
	if idxQues != -1 && idxQues < minIdx {
		minIdx = idxQues
	}
	if idxNewLine != -1 && idxNewLine < minIdx {
		minIdx = idxNewLine
	}

	if minIdx < len(s) {
		return s[:minIdx+1]
	}
	return s
}
