package video

import (
	"fmt"
	"os"

	"github.com/baalimago/clai/internal/video/generic"
)

// The configuration types are defined in video/generic so the vendor
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

var Default = Configurations{
	Model:        "sora-2",
	PromptFormat: "%v",
	Output: Output{
		Type:   UNSET,
		Dir:    fmt.Sprintf("%v/Videos", os.Getenv("HOME")),
		Prefix: "clai",
	},
}

func ValidateOutputType(outputType OutputType) error {
	switch outputType {
	case URL, LOCAL, UNSET:
		return nil
	default:
		return fmt.Errorf("invalid output type: %v", outputType)
	}
}
