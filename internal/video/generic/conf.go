// Package generic holds video's vendor-shared configuration types below
// the vendor clients, so both vendors and internal/video can import them
// (same layering as text with pkg/text/models).
package generic

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

type OutputType string

const (
	LOCAL OutputType = "local"
	URL   OutputType = "url"
	UNSET OutputType = "unset"
)
