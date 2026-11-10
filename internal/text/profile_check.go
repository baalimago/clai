package text

import (
	"fmt"
	"os"
	"strings"

	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/profile"
)

// ProfileEnvFunc builds the profile checker's environment over a config dir.
type ProfileEnvFunc func(confDir string) (profile.Env, error)

// checkProfileBody refuses a local -prp body the checker rejects, so nothing
// runs from a file that is not a runnable profile. A body that came from a
// remote target was checked by the resolver that fetched it, and a profile
// named with -p is loaded the way it always was.
func checkProfileBody(confDir string, tConf *Configurations, envFor ProfileEnvFunc) error {
	if tConf.ProfilePath == "" || tConf.ProfileFetched || envFor == nil {
		return nil
	}
	body, err := os.ReadFile(tConf.ProfilePath)
	if err != nil {
		return diag.Errorf("read profile %s: %w", tConf.ProfilePath, err)
	}
	env, err := envFor(confDir)
	if err != nil {
		return err
	}
	findings, err := profile.Validate(body, profile.File, env)
	if err != nil {
		return diag.Errorf("check profile %s: %w", tConf.ProfilePath, err)
	}
	problems := findings.Problems(profile.File)
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%s is not a valid profile:\n  %s", tConf.ProfilePath, strings.Join(problems, "\n  "))
}
