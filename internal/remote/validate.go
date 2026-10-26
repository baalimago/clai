package remote

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/baalimago/clai/internal/tools/mcp/serverconfig"
)

// MaxBodyBytes caps a remote profile. The hub refuses a larger body too.
const MaxBodyBytes = 64 * 1024

// ProfileKeys are the keys clai's text.Profile reads, plus source, the
// stamp `clai profiles install` writes. text's tests keep the two in step.
var ProfileKeys = []string{
	"name", "model", "use_tools", "use_skills", "tools", "cmd-ban", "prompt",
	"save-reply-as-conv", "mcp_servers", "shell_context", "use_lookback", "source",
	"skills",
}

// InvalidProfileError lists every problem found in a remote profile.
type InvalidProfileError struct {
	Target   string
	Problems []string
}

func (e *InvalidProfileError) Error() string {
	return fmt.Sprintf("%s is not a valid profile:\n  %s", e.Target, strings.Join(e.Problems, "\n  "))
}

// ExitCode marks a refused remote profile apart from a failed run.
func (e *InvalidProfileError) ExitCode() int { return ExitRefused }

// Checked is what Validate learned about a profile that passed.
type Checked struct {
	// Ignored are keys clai does not read and that look like no key it
	// does; they are left alone, as a local profile's would be.
	Ignored []string
}

// Validate checks a remote profile strictly, unlike the lenient local
// loader: one JSON object, no key that looks like a misspelt profile key,
// an MCP server that names a command or a url, and no relative envfile,
// which has no directory to be relative to once the profile is fetched.
func Validate(target string, body []byte) (Checked, error) {
	invalid := func(problems ...string) error {
		return &InvalidProfileError{Target: target, Problems: problems}
	}
	if len(body) > MaxBodyBytes {
		return Checked{}, invalid(fmt.Sprintf("larger than %d KiB", MaxBodyBytes/1024))
	}
	var fields map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(body))
	if err := dec.Decode(&fields); err != nil || fields == nil {
		return Checked{}, invalid("not one JSON object")
	}
	if dec.More() {
		return Checked{}, invalid("not one JSON object: more follows the first")
	}
	var problems []string
	var checked Checked
	for _, key := range sortedKeys(fields) {
		if slices.Contains(ProfileKeys, key) {
			continue
		}
		if near := nearestKey(key); near != "" {
			problems = append(problems, fmt.Sprintf("%q: unknown key (did you mean %q?)", key, near))
			continue
		}
		checked.Ignored = append(checked.Ignored, key)
	}
	if raw, ok := fields["mcp_servers"]; ok {
		problems = append(problems, serverProblems(raw)...)
	}
	if raw, ok := fields["skills"]; ok {
		if _, err := skillTargets(raw); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if len(problems) > 0 {
		return Checked{}, invalid(problems...)
	}
	return checked, nil
}

type serverView struct {
	Command string          `json:"command"`
	URL     string          `json:"url"`
	EnvFile string          `json:"envfile"`
	Env     json.RawMessage `json:"env"`
	Auth    json.RawMessage `json:"auth"`
}

func serverProblems(raw json.RawMessage) []string {
	var servers map[string]json.RawMessage
	if err := json.Unmarshal(raw, &servers); err != nil {
		return []string{"mcp_servers: needs an object of named servers"}
	}
	var problems []string
	for _, name := range sortedKeys(servers) {
		var s serverView
		if err := json.Unmarshal(servers[name], &s); err != nil {
			problems = append(problems, fmt.Sprintf("mcp_servers.%s: needs an object", name))
			continue
		}
		if strings.TrimSpace(s.Command) == "" && strings.TrimSpace(s.URL) == "" {
			problems = append(problems, fmt.Sprintf("mcp_servers.%s: needs \"command\" or \"url\"", name))
		}
		if relativeEnvFile(s.EnvFile) {
			problems = append(problems, fmt.Sprintf(
				"mcp_servers.%s.envfile: a relative path has no meaning for a remote profile; use ~/ or an absolute path", name))
		}
		if s.Auth != nil {
			problems = append(problems, authProblems(name, s.Auth, s.Env)...)
		}
	}
	return problems
}

// authProblems checks a server's auth block. Only required_env is checked
// inside it: every name valid, listed once, and not also set by the
// server's own env map, which would shadow the environment it names.
func authProblems(server string, rawAuth, rawEnv json.RawMessage) []string {
	var auth map[string]json.RawMessage
	if err := json.Unmarshal(rawAuth, &auth); err != nil || auth == nil {
		return []string{fmt.Sprintf("mcp_servers.%s.auth: needs an object", server)}
	}
	rawRequired, ok := auth["required_env"]
	if !ok {
		return nil
	}
	field := fmt.Sprintf("mcp_servers.%s.auth.required_env", server)
	var required []string
	if err := json.Unmarshal(rawRequired, &required); err != nil || required == nil {
		return []string{field + ": needs an array of variable names"}
	}
	// env's own shape is not this check's to judge; an unreadable one sets
	// nothing.
	var env map[string]json.RawMessage
	_ = json.Unmarshal(rawEnv, &env)
	var problems []string
	seen := make(map[string]bool, len(required))
	for _, name := range required {
		switch {
		case !serverconfig.ValidEnvName(name):
			problems = append(problems, fmt.Sprintf("%s: %q is not a valid environment variable name", field, name))
		case seen[name]:
			problems = append(problems, fmt.Sprintf("%s: %q is listed twice", field, name))
		case env[name] != nil:
			problems = append(problems, fmt.Sprintf("%s: %q is also set in mcp_servers.%s.env, so the environment never supplies it", field, name, server))
		}
		seen[name] = true
	}
	return problems
}

// relativeEnvFile reports an envfile that the loader would resolve against
// the profile's directory: not absolute, not ~/, and no variable to expand.
func relativeEnvFile(p string) bool {
	p = strings.TrimSpace(p)
	if p == "" || filepath.IsAbs(p) || p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "$") {
		return false
	}
	return true
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// nearestKey returns the profile key within two edits of key, or "".
func nearestKey(key string) string {
	best, bestDist := "", 3
	for _, k := range ProfileKeys {
		if d := editDistance(strings.ToLower(key), k); d < bestDist {
			best, bestDist = k, d
		}
	}
	return best
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}
