package photo

import (
	"fmt"

	"github.com/baalimago/clai/internal/modelalias"
	"github.com/baalimago/clai/internal/photo/generic"
	"github.com/baalimago/clai/internal/vendors/gemini"
	"github.com/baalimago/clai/internal/vendors/openai"
	"github.com/baalimago/clai/internal/vendors/openrouter"
)

type (
	Configurations = generic.Configurations
	Output         = generic.Output
	OutputType     = generic.OutputType
)

const (
	LOCAL = generic.LOCAL
	URL   = generic.URL
	UNSET = generic.UNSET
)

var DEFAULT = Configurations{
	Model: openai.DefaultImageModel,
	// The shipped aliases are a starting point the user owns from the first
	// write; each lists one model per image vendor so a machine with any one
	// key still answers (architecture/model-aliases.md).
	ModelAliases: modelalias.Table{
		"strong": {openai.DefaultImageModel, gemini.DefaultImageModel, "or:" + openrouter.DefaultImageModel},
		"fast":   {gemini.DefaultImageModel, openai.DefaultImageModel},
	},
	PromptFormat: "I NEED to test how the tool works with extremely simple prompts. DO NOT add any detail, just use it AS-IS: '%v'",
	Output: Output{
		Type:   UNSET,
		Dir:    "~/Pictures",
		Prefix: "clai",
	},
}

// ValidateOutputType is kind of dumb. Why did I add this..?
func ValidateOutputType(outputType OutputType) error {
	switch outputType {
	case URL, LOCAL, UNSET:
		return nil
	default:
		return fmt.Errorf("invalid output type: %v", outputType)
	}
}
