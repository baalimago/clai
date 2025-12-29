package text

import (
	"testing"

	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

func Test_handleFunctionCall(t *testing.T) {
	q := Querier[mockCompleter]{
		remainingToolCalls: misc.Pointer(5),
	}

	_ = q
}
