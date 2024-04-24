package photo

import (
	"fmt"
	"os"
)

type Configurations struct {
	Model string `json:"model"`
	// Format of the prompt, will place prompt at '%v'
	PromptFormat string `json:"prompt-format"`
	Output       Output `json:"output"`
	Raw          bool   `json:"raw"`
	StdinReplace string `json:"-"`
	ReplyMode    bool   `json:"-"`
	Prompt       string `json:"-"`
}

type Output struct {
	Type   OutputType `json:"type"`
	Dir    string     `json:"dir"`
	Prefix string     `json:"prefix"`
}

var DEFAULT = Configurations{
	Model:        "dall-e-3",
	PromptFormat: "I NEED to test how the tool works with extremely simple prompts. DO NOT add any detail, just use it AS-IS: '%v'",
	Output: Output{
		Type:   LOCAL,
		Dir:    fmt.Sprintf("%v/Pictures", os.Getenv("HOME")),
		Prefix: "clai",
	},
}

type OutputType string

const (
	URL   OutputType = "url"
	LOCAL OutputType = "local"
)

func ValidateOutputType(outputType OutputType) error {
	switch outputType {
	case URL, LOCAL:
		return nil
	default:
		return fmt.Errorf("invalid output type: %v", outputType)
	}
}
