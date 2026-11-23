package sync

import (
	"bytes"
	"cmp"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/baalimago/clai/internal/diag"
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
	var stamp forkStamp
	if device != "" {
		var doc map[string]json.RawMessage
		_ = json.Unmarshal(theirs, &doc)
		stamp.Title = strings.TrimSpace(titleOf(doc) + " (" + device + ")")
	}
	return forkCopy(rel, theirs, newChatID(now), stamp)
}

// forkStamp is what a fork sets on its copy beyond the fork marker.
type forkStamp struct {
	// Title replaces the title when set.
	Title string
	// Created replaces the creation time when not zero.
	Created time.Time
}

// forkCopy turns raw, the chat at rel, into a new chat id that names rel's chat as its parent (I3).
func forkCopy(rel string, raw []byte, id string, stamp forkStamp) (string, []byte, string, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", nil, "", diag.Errorf("fork %s: %w", rel, err)
	}
	title := cmp.Or(stamp.Title, titleOf(doc))
	parent := strings.TrimSuffix(path.Base(rel), ".json")
	members := map[string]any{"id": id, "source": "clai", "source_id": parent, "title": title}
	if !stamp.Created.IsZero() {
		members["created"] = stamp.Created
	}
	for k, v := range members {
		b, err := json.Marshal(v)
		if err != nil {
			return "", nil, "", err
		}
		doc[k] = b
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return "", nil, "", err
	}
	return path.Join("conversations", id+".json"), b, title, nil
}

func titleOf(doc map[string]json.RawMessage) string {
	var title string
	_ = json.Unmarshal(doc["title"], &title)
	if title == "" {
		title = firstUserText(doc["messages"])
	}
	return title
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
