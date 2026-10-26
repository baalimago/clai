package sync

import (
	"encoding/json"
	"net/url"
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
var secretValue = regexp.MustCompile(`(sk-[A-Za-z0-9_-]{20,}|sk_(live|test)_[A-Za-z0-9]{16,}|ghp_[A-Za-z0-9]{30,}|gho_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{30,}|xox[bpars]-[A-Za-z0-9-]{10,}|hf_[A-Za-z0-9]{30,}|gsk_[A-Za-z0-9]{40,}|xai-[A-Za-z0-9]{40,}|glpat-[A-Za-z0-9_-]{20,}|ntn_[A-Za-z0-9]{40,}|AKIA[0-9A-Z]{16}|AIza[0-9A-Za-z_-]{35}|-----BEGIN [A-Z ]*PRIVATE KEY-----)`)

// secretSuffixes end a field name that holds a credential: GITHUB_TOKEN,
// x-api-key and apiKey match; max_tokens, token_env and monkey do not.
var secretSuffixes = []string{"apikey", "key", "token", "secret", "password", "passwd", "pass", "authorization", "credentials", "credential", "cookie"}

// joinedSuffixes end an all-caps name with no separator, as GITHUBTOKEN or
// PGPASS; KEY is left out, since MONKEY-shaped names are common.
var joinedSuffixes = []string{"APIKEY", "TOKEN", "SECRET", "PASSWORD", "PASSWD", "PASS"}

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
				if (secretName(k) && !reference(s)) || (inEnv && literal(s, opaqueEnvMin) && !isPath(s)) || urlSecret(s) {
					return Secret{Field: field}, true
				}
				continue
			}
			if found, ok := secretIn(val, field, strings.EqualFold(k, "env")); ok {
				return found, true
			}
		}
	case []any:
		prev := ""
		for _, val := range t {
			if s, ok := val.(string); ok {
				if argSecret(prev, s) || urlSecret(s) {
					return Secret{Field: path}, true
				}
				prev = s
				continue
			}
			prev = ""
			if found, ok := secretIn(val, path, false); ok {
				return found, true
			}
		}
	}
	return Secret{}, false
}

// argSecret: a command-line credential, as "--api-key", "<key>" or
// "--token=<key>".
func argSecret(prev, s string) bool {
	if name, value, ok := strings.Cut(strings.TrimLeft(s, "-"), "="); ok && strings.HasPrefix(s, "-") {
		return secretName(name) && !reference(value)
	}
	if !strings.HasPrefix(prev, "-") || strings.HasPrefix(s, "-") {
		return false
	}
	return secretName(strings.TrimLeft(prev, "-")) && !reference(s)
}

// urlSecret: a URL that carries a credential in its user info or in a
// query parameter named like one.
func urlSecret(s string) bool {
	if !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
		return false
	}
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil {
		return false
	}
	if pw, ok := u.User.Password(); ok && pw != "" {
		return true
	}
	for k, vals := range u.Query() {
		for _, v := range vals {
			if secretName(k) && !reference(v) {
				return true
			}
		}
	}
	return false
}

// isPath: a home or absolute path is an env setting however long it is.
func isPath(s string) bool {
	return strings.HasPrefix(s, "/") || strings.HasPrefix(s, "~/")
}

func secretName(k string) bool {
	if k != "" && k == strings.ToUpper(k) {
		for _, suffix := range joinedSuffixes {
			if strings.HasSuffix(k, suffix) {
				return true
			}
		}
	}
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
// reference to one.
func literal(s string, minLen int) bool {
	s = strings.TrimSpace(s)
	return len(s) >= minLen && !strings.ContainsAny(s, " \t\n") && !reference(s)
}

// reference: a blank value, or one that names a credential instead of
// holding it ("$OPENAI_API_KEY", "${TOKEN}", "env:NAME") or is a URL. Any
// other value under a secret's name is the secret, however short.
func reference(s string) bool {
	s = strings.TrimSpace(s)
	if b, ok := strings.CutPrefix(s, "Bearer "); ok {
		s = strings.TrimSpace(b)
	}
	if s == "" {
		return true
	}
	for _, ref := range []string{"$", "env:", "http://", "https://"} {
		if strings.HasPrefix(s, ref) {
			return true
		}
	}
	return false
}
