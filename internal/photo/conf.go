package photo

import (
	"fmt"
	"os"

	"github.com/baalimago/clai/internal/photo/generic"
)

// The configuration types are defined in photo/generic so the vendor
// clients can import them without importing this package (which would
// cycle: this package composes vendors).
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
	Model:        "gpt-image-1",
	PromptFormat: "I NEED to test how the tool works with extremely simple prompts. DO NOT add any detail, just use it AS-IS: '%v'",
	Output: Output{
		Type:   UNSET,
		Dir:    fmt.Sprintf("%v/Pictures", os.Getenv("HOME")),
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
