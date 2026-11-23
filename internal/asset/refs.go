package asset

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
)

// WholeFile is the RefScanError.Message of a failure that is not one message's.
const WholeFile = -1

// RefScanError is a chat file whose refs could not be read.
type RefScanError struct {
	Message int
	Cause   error
}

func (e *RefScanError) Error() string {
	if e.Message == WholeFile {
		return "scan for asset refs: " + e.Cause.Error()
	}
	return fmt.Sprintf("scan message %d for asset refs: %s", e.Message, e.Cause)
}

func (e *RefScanError) Unwrap() error { return e.Cause }

// RefsIn lists the image refs a chat file references, sorted and each once (RefsIn.1).
func RefsIn(conversation []byte) ([]string, error) {
	var scan struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(conversation, &scan); err != nil {
		return nil, &RefScanError{Message: WholeFile, Cause: err}
	}
	seen := map[string]struct{}{}
	for i, m := range scan.Messages {
		if !bytes.HasPrefix(bytes.TrimSpace(m.Content), []byte("[")) {
			continue
		}
		var parts []struct {
			Type  string `json:"type"`
			Asset string `json:"asset"`
		}
		if err := json.Unmarshal(m.Content, &parts); err != nil {
			return nil, &RefScanError{Message: i, Cause: err}
		}
		for _, part := range parts {
			if part.Type == "image" && part.Asset != "" {
				seen[part.Asset] = struct{}{}
			}
		}
	}
	refs := make([]string, 0, len(seen))
	for ref := range seen {
		refs = append(refs, ref)
	}
	slices.Sort(refs)
	if len(refs) == 0 {
		return nil, nil
	}
	return refs, nil
}
