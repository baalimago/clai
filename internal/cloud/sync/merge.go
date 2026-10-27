package sync

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
)

// chatFile matches a top-level conversation: the one kind of file a pull
// merges on its own.
var chatFile = regexp.MustCompile(`^conversations/[^/]+\.json$`)

type mergeOutcome int

const (
	keepRemote mergeOutcome = iota
	keepLocal
	keepBoth
	// replaceLocal is two copies with the same messages that differ
	// elsewhere, such as a title: theirs is taken and mine goes to the trash.
	replaceLocal
)

type chatShape struct {
	Messages []json.RawMessage `json:"messages"`
	Title    string            `json:"title"`
}

// mergeChats compares two copies of one conversation by their messages:
// when one only adds messages to the other, the longer one wins.
func mergeChats(mine, theirs []byte) mergeOutcome {
	var a, b chatShape
	if json.Unmarshal(mine, &a) != nil || json.Unmarshal(theirs, &b) != nil {
		return keepBoth
	}
	shorter, longer := a.Messages, b.Messages
	if len(a.Messages) > len(b.Messages) {
		shorter, longer = b.Messages, a.Messages
	}
	for i := range shorter {
		if !jsonEqual(shorter[i], longer[i]) {
			return keepBoth
		}
	}
	switch {
	case len(a.Messages) > len(b.Messages):
		return keepLocal
	case len(a.Messages) < len(b.Messages):
		return keepRemote
	case a.Title != "" && b.Title == "":
		return keepLocal
	case jsonEqual(mine, theirs):
		return keepRemote
	}
	return replaceLocal
}

func jsonEqual(x, y json.RawMessage) bool {
	var bx, by bytes.Buffer
	if json.Compact(&bx, x) != nil || json.Compact(&by, y) != nil {
		return bytes.Equal(x, y)
	}
	return bytes.Equal(bx.Bytes(), by.Bytes())
}

// Fork is a conversation continued on two machines: the other machine's
// copy, written beside this one's as a new chat.
type Fork struct {
	Path   string `json:"path"`
	Forked string `json:"forked"`
	Title  string `json:"title"`
}

// forkTheirs writes the server's diverged copy of rel as a new chat that
// names the one it split from (source "clai", the fork marker clai already
// uses) and the device it came from. This machine's file is untouched.
func forkTheirs(rel string, theirs []byte, device string, now time.Time) (string, []byte, string, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(theirs, &doc); err != nil {
		return "", nil, "", fmt.Errorf("fork %s: %w", rel, err)
	}
	var title string
	_ = json.Unmarshal(doc["title"], &title)
	if title == "" {
		title = firstUserText(doc["messages"])
	}
	if device != "" {
		title = strings.TrimSpace(title + " (" + device + ")")
	}
	id := newChatID(now)
	parent := strings.TrimSuffix(path.Base(rel), ".json")
	for k, v := range map[string]string{"id": id, "source": "clai", "source_id": parent, "title": title} {
		b, _ := json.Marshal(v)
		doc[k] = b
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return "", nil, "", err
	}
	return path.Join("conversations", id+".json"), b, title, nil
}

func firstUserText(raw json.RawMessage) string {
	var msgs []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &msgs) != nil {
		return ""
	}
	for _, m := range msgs {
		var s string
		if m.Role == "user" && json.Unmarshal(m.Content, &s) == nil {
			s = strings.Join(strings.Fields(s), " ")
			if len(s) > 40 {
				s = s[:40]
			}
			return s
		}
	}
	return ""
}

// newChatID matches internal/chat.NewChatID's shape.
func newChatID(now time.Time) string {
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x-%s", now.Unix(), hex.EncodeToString(b))
}
