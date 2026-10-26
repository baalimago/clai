package cloud

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Config files that hold a credential are never uploaded: the CLI tells
// the user to move it to an env var or an envfile instead. The check errs
// toward skipping; a false positive costs one file not syncing, a false
// negative leaks a key.
var (
	// A key that ends in one of these words: GITHUB_TOKEN and x-api-key
	// match, max_tokens and token_env do not.
	secretKey = regexp.MustCompile(`(?i)(^|[_-])(api[_-]?key|apikey|secret|token|password|passwd|authorization|bearer|credentials?|private[_-]?key)$`)
	// Shapes that are a key whatever field holds them.
	secretValue = regexp.MustCompile(`(sk-[A-Za-z0-9_-]{20,}|sk_(live|test)_[A-Za-z0-9]{16,}|ghp_[A-Za-z0-9]{30,}|gho_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{30,}|xox[bpars]-[A-Za-z0-9-]{10,}|AKIA[0-9A-Z]{16}|AIza[0-9A-Za-z_-]{35}|-----BEGIN [A-Z ]*PRIVATE KEY-----)`)
)

// holdsSecret reports whether a config file's bytes look like they carry a
// credential, and names the field when it can.
func holdsSecret(name string, b []byte) (bool, string) {
	if m := secretValue.Find(b); m != nil {
		return true, "a value shaped like a key"
	}
	if !strings.HasSuffix(name, ".json") {
		return false, ""
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return false, ""
	}
	return secretField(v)
}

func secretField(v any) (bool, string) {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if s, ok := val.(string); ok && secretKey.MatchString(k) && literalSecret(s) {
				return true, k
			}
			if found, field := secretField(val); found {
				return true, field
			}
		}
	case []any:
		for _, val := range t {
			if found, field := secretField(val); found {
				return true, field
			}
		}
	}
	return false, ""
}

// literalSecret: a value long enough to be a key and not a reference to
// one ("$OPENAI_API_KEY", "${TOKEN}", "env:NAME") or a URL.
func literalSecret(s string) bool {
	s = strings.TrimSpace(s)
	if b, ok := strings.CutPrefix(s, "Bearer "); ok {
		s = b
	}
	if len(s) < 8 || strings.ContainsAny(s, " \t\n") {
		return false
	}
	for _, ref := range []string{"$", "env:", "http://", "https://"} {
		if strings.HasPrefix(s, ref) {
			return false
		}
	}
	return true
}
