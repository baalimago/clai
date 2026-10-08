package text

import (
	"strings"

	"github.com/baalimago/clai/internal/text/generic"
	"github.com/baalimago/clai/internal/vendors/jev"
)

var _ generic.ErrorHandler = (*jev.Jev)(nil)

func vendorErrorHandler(model string) generic.ErrorHandler {
	if !strings.HasPrefix(model, "jev-") {
		return nil
	}
	j := jev.Default
	return &j
}
