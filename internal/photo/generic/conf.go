// Package generic holds photo's configuration types, its -re reply context
// and the progress animation.
package generic

type Configurations struct {
	Model string `json:"model"`
	// Format of the prompt, will place prompt at '%v'
	PromptFormat string `json:"prompt-format"`
	Output       Output `json:"output"`
	Raw          bool   `json:"raw"`
	// AutoOpenImages opens each saved image with ImageOpener, as -open does.
	AutoOpenImages bool `json:"auto-open-images" migrate:"true"`
	// ImageOpener is the viewer command; "" means the platform default.
	ImageOpener  string `json:"image-opener" migrate:"true"`
	StdinReplace string `json:"-"`
	ReplyMode    bool   `json:"-"`
	Prompt       string `json:"-"`
}

type Output struct {
	Type   OutputType `json:"type"`
	Dir    string     `json:"dir"`
	Prefix string     `json:"prefix"`
}

type OutputType string

const (
	LOCAL OutputType = "local"
	URL   OutputType = "url"
	UNSET OutputType = "unset"
)
