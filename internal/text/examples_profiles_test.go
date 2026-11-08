package text

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"

	"github.com/baalimago/clai/internal/profile"
)

// pinnedPackage matches an npm package with an exact @version or a PyPI
// package with an exact ==version.
var pinnedPackage = regexp.MustCompile(`^(@[^/\s]+/)?[^@=\s-][^@=\s]*(@|==)\d[^\s]*$`)

var packageRunners = []string{"npx", "pnpx", "bunx", "uvx"}

func TestExampleProfiles(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "examples", "profiles", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no example profiles found")
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var p Profile
			if err := json.Unmarshal(raw, &p); err != nil {
				t.Fatalf("not a profile: %v", err)
			}
			// The hub serves these as remote profiles, which are checked
			// strictly on the Remote route.
			findings, err := profile.Validate(raw, profile.Remote, profile.Env{})
			if err != nil {
				t.Fatalf("check %s: %v", path, err)
			}
			if problems := findings.Problems(profile.Remote); len(problems) > 0 {
				t.Fatalf("%s is refused on the Remote route: %v", path, problems)
			}
			if p.Model == "" || p.Prompt == "" {
				t.Fatal("profile needs a model and a prompt")
			}
			for name, s := range p.McpServers {
				if (s.Command == "") == (s.Url == "") {
					t.Fatalf("mcp server %q: set exactly one of command and url", name)
				}
				if !slices.Contains(packageRunners, s.Command) {
					continue
				}
				if !slices.ContainsFunc(s.Args, pinnedPackage.MatchString) {
					t.Fatalf("mcp server %q: package is not pinned to a version: %v", name, s.Args)
				}
			}
		})
	}
}
