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

// hubKeys are fields the hub reads and clai does not, such as the
// description its page shows: expected in a published profile, so never
// listed as ignored.
var hubKeys = []string{"description"}

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
// every field Summarize reads of the right shape, an MCP server that names
// a command or a url, no relative envfile, which has no directory to be
// relative to once the profile is fetched, and a well-formed auth block.
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
		if slices.Contains(ProfileKeys, key) || slices.Contains(hubKeys, key) {
			continue
		}
		if near := nearestKey(key, ProfileKeys); near != "" {
			problems = append(problems, fmt.Sprintf("%q: unknown key (did you mean %q?)", key, near))
			continue
		}
		checked.Ignored = append(checked.Ignored, key)
	}
	problems = append(problems, topLevelProblems(fields)...)
	if raw, ok := fields["mcp_servers"]; ok {
		serverIssues, ignored := serverProblems(raw)
		problems = append(problems, serverIssues...)
		checked.Ignored = append(checked.Ignored, ignored...)
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

// authKeys are the auth keys clai reads.
var authKeys = []string{"token_command", "token_env", "required_env", "scopes"}

// fieldKind is the JSON shape a field must have for clai to read it.
type fieldKind int

const (
	kindString fieldKind = iota
	kindStrings
	kindBool
	kindStringMap
)

// decodes reports whether raw decodes as kind. An absent field always does.
func decodes(raw json.RawMessage, kind fieldKind) bool {
	if raw == nil {
		return true
	}
	var err error
	switch kind {
	case kindString:
		var v string
		err = json.Unmarshal(raw, &v)
	case kindStrings:
		var v []string
		err = json.Unmarshal(raw, &v)
	case kindBool:
		var v bool
		err = json.Unmarshal(raw, &v)
	case kindStringMap:
		var v map[string]string
		err = json.Unmarshal(raw, &v)
	}
	return err == nil
}

// topLevelProblems type-checks the top-level fields Summarize reads, so a
// body Validate accepts can always be summarised.
func topLevelProblems(fields map[string]json.RawMessage) []string {
	var problems []string
	for _, key := range []string{"model", "prompt"} {
		if !decodes(fields[key], kindString) {
			problems = append(problems, fmt.Sprintf("%q: needs a string", key))
		}
	}
	if raw, ok := fields["model"]; ok {
		var model string
		if json.Unmarshal(raw, &model) == nil && IsRemote(model) {
			if _, err := ParseModelListTarget(model); err != nil {
				problems = append(problems, fmt.Sprintf("model: %v", err))
			}
		}
	}
	if !decodes(fields["use_tools"], kindBool) {
		problems = append(problems, `"use_tools": needs true or false`)
	}
	if !decodes(fields["tools"], kindStrings) {
		problems = append(problems, `"tools": needs an array of strings`)
	}
	return problems
}

func serverProblems(raw json.RawMessage) (problems, ignored []string) {
	var servers map[string]json.RawMessage
	if err := json.Unmarshal(raw, &servers); err != nil {
		return []string{"mcp_servers: needs an object of named servers"}, nil
	}
	for _, name := range sortedKeys(servers) {
		prefix := "mcp_servers." + name
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(servers[name], &fields); err != nil || fields == nil {
			problems = append(problems, prefix+": needs an object")
			continue
		}
		for _, key := range []string{"command", "url", "envfile"} {
			if !decodes(fields[key], kindString) {
				problems = append(problems, fmt.Sprintf("%s.%s: needs a string", prefix, key))
			}
		}
		if !decodes(fields["args"], kindStrings) {
			problems = append(problems, prefix+".args: needs an array of strings")
		}
		if !decodes(fields["env"], kindStringMap) {
			problems = append(problems, prefix+".env: needs an object of string values")
		}
		var command, url, envFile string
		_ = json.Unmarshal(fields["command"], &command)
		_ = json.Unmarshal(fields["url"], &url)
		_ = json.Unmarshal(fields["envfile"], &envFile)
		if strings.TrimSpace(command) == "" && strings.TrimSpace(url) == "" {
			problems = append(problems, prefix+`: needs "command" or "url"`)
		}
		if relativeEnvFile(envFile) {
			problems = append(problems, prefix+".envfile: a relative path has no meaning for a remote profile; use ~/ or an absolute path")
		}
		if rawAuth, ok := fields["auth"]; ok {
			p, i := authProblems(name, rawAuth, fields["env"])
			problems = append(problems, p...)
			ignored = append(ignored, i...)
		}
	}
	return problems, ignored
}

// authProblems checks a server's auth block: every field Summarize reads is
// the right shape, token_env and every required_env entry is a variable
// name, a required name is listed once and not also set by the server's
// own env map, which would shadow the environment it names. An entry that
// is not a name is reported by index or field, never echoed: it may be a
// pasted secret, and it would be printed in the trust prompt.
func authProblems(server string, rawAuth, rawEnv json.RawMessage) (problems, ignored []string) {
	prefix := fmt.Sprintf("mcp_servers.%s.auth", server)
	var auth map[string]json.RawMessage
	if err := json.Unmarshal(rawAuth, &auth); err != nil || auth == nil {
		return []string{prefix + ": needs an object"}, nil
	}
	for _, key := range sortedKeys(auth) {
		if slices.Contains(authKeys, key) {
			continue
		}
		if near := nearestKey(key, authKeys); near != "" {
			problems = append(problems, fmt.Sprintf("%s: %q: unknown key (did you mean %q?)", prefix, key, near))
			continue
		}
		ignored = append(ignored, prefix+"."+key)
	}
	if raw, ok := auth["token_env"]; ok {
		var tokenEnv string
		switch {
		case json.Unmarshal(raw, &tokenEnv) != nil:
			problems = append(problems, prefix+".token_env: needs a variable name")
		case !serverconfig.ValidEnvName(tokenEnv):
			problems = append(problems, prefix+".token_env is not a valid environment variable name")
		}
	}
	if raw, ok := auth["token_command"]; ok {
		var argv []string
		if json.Unmarshal(raw, &argv) != nil || len(argv) == 0 {
			problems = append(problems, prefix+".token_command: needs a non-empty array of strings")
		}
	}
	if !decodes(auth["scopes"], kindStrings) {
		problems = append(problems, prefix+".scopes: needs an array of strings")
	}
	if raw, ok := auth["required_env"]; ok {
		problems = append(problems, requiredEnvProblems(server, raw, rawEnv)...)
	}
	return problems, ignored
}

func requiredEnvProblems(server string, raw, rawEnv json.RawMessage) []string {
	field := fmt.Sprintf("mcp_servers.%s.auth.required_env", server)
	var required []string
	if err := json.Unmarshal(raw, &required); err != nil || required == nil {
		return []string{field + ": needs an array of variable names"}
	}
	// env's own shape is not this check's to judge; an unreadable one sets
	// nothing.
	var env map[string]json.RawMessage
	_ = json.Unmarshal(rawEnv, &env)
	var problems []string
	first := make(map[string]int, len(required))
	for i, name := range required {
		at := fmt.Sprintf("%s[%d]", field, i)
		j, seen := first[name]
		switch {
		case !serverconfig.ValidEnvName(name):
			problems = append(problems, at+" is not a valid environment variable name")
		case seen:
			problems = append(problems, fmt.Sprintf("%s repeats required_env[%d]", at, j))
		case env[name] != nil:
			problems = append(problems, fmt.Sprintf("%s is also a key of mcp_servers.%s.env, so the environment never supplies it", at, server))
		}
		if !seen {
			first[name] = i
		}
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

// nearestKey returns the one of keys within two edits of key, or "".
func nearestKey(key string, keys []string) string {
	best, bestDist := "", 3
	for _, k := range keys {
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
