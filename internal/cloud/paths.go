package cloud

import (
	"regexp"
	"strings"
)

// Store names one of the two synced stores. The values are the server's.
type Store string

const (
	StoreConfig        Store = "config"
	StoreConversations Store = "conversations"
)

// Stores lists every store in sync order: config first, since a profile a
// conversation names should arrive before the conversation.
var Stores = []Store{StoreConfig, StoreConversations}

// ParseStore maps a command-line name to a store.
func ParseStore(s string) (Store, bool) {
	switch Store(s) {
	case StoreConfig, StoreConversations:
		return Store(s), true
	}
	return "", false
}

// The allowlists mirror clai-cloud's cloud/site/sync.ts validPath. A path
// is relative to the clai config dir and uses "/". Keep both in step: a
// path the server refuses fails the whole commit.
var (
	configPaths = []*regexp.Regexp{
		regexp.MustCompile(`^(textConfig|photoConfig|videoConfig|theme|skills)\.json$`),
		regexp.MustCompile(`^(profiles|mcpServers|shellContexts)/[^/]+\.json$`),
		regexp.MustCompile(`^skills/.+$`),
	}
	conversationPaths = []*regexp.Regexp{
		regexp.MustCompile(`^conversations/[^/]+\.json$`),
		regexp.MustCompile(`^conversations/assets/[0-9a-f]{64}\.(png|jpg|gif|webp)$`),
		regexp.MustCompile(`^conversations/reasoning/[^/]+/[^/]+\.json$`),
		regexp.MustCompile(`^conversations/imported/[a-z0-9][a-z0-9-]{0,31}/[^/]+\.json$`),
	}
	// Go's regexp has no lookahead, so the two per-machine conversation
	// files the server's pattern excludes are listed here.
	conversationExcluded = map[string]bool{
		"conversations/globalScope.json": true,
		"conversations/prevQuery.json":   true,
	}
)

// Allowed reports whether rel may live in store.
func Allowed(store Store, rel string) bool {
	if rel == "" || len(rel) > 512 || strings.ContainsAny(rel, "\\\x00") {
		return false
	}
	for _, r := range rel {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == "" || strings.HasPrefix(seg, ".") || len(seg) > 255 {
			return false
		}
	}
	patterns := configPaths
	if store == StoreConversations {
		if conversationExcluded[rel] {
			return false
		}
		patterns = conversationPaths
	}
	for _, p := range patterns {
		if p.MatchString(rel) {
			return true
		}
	}
	return false
}

// roots are the directories under the config dir a store's scan walks;
// a top-level file is matched by name.
func roots(store Store) []string {
	if store == StoreConfig {
		return []string{".", "profiles", "mcpServers", "shellContexts", "skills"}
	}
	return []string{"conversations"}
}
