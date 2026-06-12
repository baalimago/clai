package skills

import (
	"os"

	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

func debugSkills() bool {
	return misc.Truthy(os.Getenv("DEBUG")) || misc.Truthy(os.Getenv("DEBUG_SKILLS"))
}
