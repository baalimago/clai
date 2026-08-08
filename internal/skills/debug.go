package skills

import "github.com/baalimago/clai/internal/debugflags"

func debugSkills() bool {
	return debugflags.Enabled("SKILLS")
}
