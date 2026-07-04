package chat

import (
	"regexp"
	"testing"
)

func TestNewChatID_FormatAndUniqueness(t *testing.T) {
	seen := map[string]struct{}{}
	re := regexp.MustCompile(`^[0-9a-f]+-[0-9a-f]{20}$`)

	for i := 0; i < 500; i++ {
		id := NewChatID()
		if !re.MatchString(id) {
			t.Fatalf("id %q did not match expected format", id)
		}
		if _, ok := seen[id]; ok {
			t.Fatalf("duplicate id generated: %q", id)
		}
		seen[id] = struct{}{}
	}
}
