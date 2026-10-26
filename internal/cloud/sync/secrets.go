package sync

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode"
)

// A config file that holds a credential is never committed: status names it
// with the field, and the user moves the value to an env var or envfile.
// The rules are one set with clai-cloud's cloud/site/secrets.ts, and both
// sides test against the same vectors (testdata/secrets.vectors.json). They
// err toward skipping: a false positive costs one file not syncing, a false
// negative leaks a key.
var secretValue = regexp.MustCompile(`(sk-[A-Za-z0-9_-]{20,}|sk_(live|test)_[A-Za-z0-9]{16,}|ghp_[A-Za-z0-9]{30,}|gho_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{30,}|xox[bpars]-[A-Za-z0-9-]{10,}|AKIA[0-9A-Z]{16}|AIza[0-9A-Za-z_-]{35}|-----BEGIN [A-Z ]*PRIVATE KEY-----)`)

// secretSuffixes end a field name that holds a credential: GITHUB_TOKEN,
// x-api-key and apiKey match; max_tokens, token_env and monkey do not.
var secretSuffixes = []string{"apikey", "key", "token", "secret", "password", "passwd", "authorization", "credentials", "credential"}

// opaqueEnvMin is the length from which an env value with no whitespace is
// taken for a key, as the hub has always refused.
const opaqueEnvMin = 20

// Secret is where a file holds a credential.
type Secret struct {
	// Field is the JSON field, or "" for a value found by its shape.
	Field string
}

func (s Secret) String() string {
	if s.Field == "" {
		return "a value shaped like a key"
	}
	return s.Field
}

// FindSecret reports the first credential in a config file's bytes.
func FindSecret(name string, b []byte) (Secret, bool) {
	if secretValue.Match(b) {
		return Secret{}, true
	}
	if !strings.HasSuffix(name, ".json") {
		return Secret{}, false
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return Secret{}, false
	}
	return secretIn(v, "", false)
}

func secretIn(v any, path string, inEnv bool) (Secret, bool) {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			field := k
			if path != "" {
				field = path + "." + k
			}
			if s, ok := val.(string); ok {
				if (secretName(k) && literal(s, 8)) || (inEnv && literal(s, opaqueEnvMin) && !isPath(s)) {
					return Secret{Field: field}, true
				}
				continue
			}
			if found, ok := secretIn(val, field, strings.EqualFold(k, "env")); ok {
				return found, true
			}
		}
	case []any:
		for _, val := range t {
			if found, ok := secretIn(val, path, false); ok {
				return found, true
			}
		}
	}
	return Secret{}, false
}

// isPath: a home or absolute path is an env setting however long it is.
func isPath(s string) bool {
	return strings.HasPrefix(s, "/") || strings.HasPrefix(s, "~/")
}

func secretName(k string) bool {
	lower := strings.ToLower(k)
	for _, suffix := range secretSuffixes {
		if !strings.HasSuffix(lower, suffix) {
			continue
		}
		prefix := k[:len(k)-len(suffix)]
		if prefix == "" || strings.ContainsAny(prefix[len(prefix)-1:], "_-.") {
			return true
		}
		// camelCase: apiKey, githubToken.
		if unicode.IsUpper(rune(k[len(prefix)])) && !unicode.IsUpper(rune(prefix[len(prefix)-1])) {
			return true
		}
	}
	return false
}

// literal: a value at least minLen long with no whitespace that is not a
// reference to one ("$OPENAI_API_KEY", "${TOKEN}", "env:NAME") or a URL.
func literal(s string, minLen int) bool {
	s = strings.TrimSpace(s)
	if b, ok := strings.CutPrefix(s, "Bearer "); ok {
		s = b
	}
	if len(s) < minLen || strings.ContainsAny(s, " \t\n") {
		return false
	}
	for _, ref := range []string{"$", "env:", "http://", "https://"} {
		if strings.HasPrefix(s, ref) {
			return false
		}
	}
	return true
}
