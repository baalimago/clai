// Package generic holds photo's vendor-shared surface — the configuration
// types, image saving and the progress animation — below the vendor
// clients, so both vendors and internal/photo can import it (same layering
// as text with pkg/text/models).
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
