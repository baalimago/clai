package video

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

	PromptImageB64 string `json:"-"`
}

type Output struct {
	Type   OutputType `json:"type"`
	Dir    string     `json:"dir"`
	Prefix string     `json:"prefix"`
}

var Default = Configurations{
	Model:        "sora-2",
	PromptFormat: "%v",
	Output: Output{
		Type:   UNSET,
		Dir:    fmt.Sprintf("%v/Videos", os.Getenv("HOME")),
		Prefix: "clai",
	},
}

type OutputType string

const (
	LOCAL OutputType = "local"
	URL   OutputType = "url"
	UNSET OutputType = "unset"
)

func ValidateOutputType(outputType OutputType) error {
	switch outputType {
	case URL, LOCAL, UNSET:
		return nil
	default:
		return fmt.Errorf("invalid output type: %v", outputType)
	}
}
