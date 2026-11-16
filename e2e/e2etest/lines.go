package e2etest

import (
	"regexp"
	"strings"
	"testing"
)

var chatRecordID = regexp.MustCompile(`("type":"chat","id":")[^"]*`)

// MaskChatIDs writes $CHAT for the id of each chat record: every run saves
// a conversation under a new id.
func MaskChatIDs(s string) string {
	return chatRecordID.ReplaceAllString(s, "${1}$$CHAT")
}

// ListingLine returns the line of a listing that names path, failing when no
// line does.
func ListingLine(t *testing.T, output, path string) string {
	t.Helper()
	for line := range strings.SplitSeq(output, "\n") {
		if strings.Contains(line, path) {
			return line
		}
	}
	t.Fatalf("no line names %q in %q", path, output)
	return ""
}
