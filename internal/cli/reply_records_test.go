package cli

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
)

type replyRecord struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func replyRecords(t *testing.T, stream string) []replyRecord {
	t.Helper()
	var records []replyRecord
	for line := range strings.Lines(stream) {
		var record replyRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("line is not a record: %q (%v)", line, err)
		}
		records = append(records, record)
	}
	return records
}

// assertRepliesCloseTheirDeltas checks that every reply equals the
// reply_delta records since the previous reply, and that no delta is left
// without its closing reply.
func assertRepliesCloseTheirDeltas(t *testing.T, stdout string) []string {
	t.Helper()
	var replies []string
	var deltas strings.Builder
	for _, record := range replyRecords(t, stdout) {
		switch record.Type {
		case "reply_delta":
			deltas.WriteString(record.Text)
		case "reply":
			if record.Text != deltas.String() {
				t.Fatalf("reply %q differs from its deltas %q", record.Text, deltas.String())
			}
			replies = append(replies, record.Text)
			deltas.Reset()
		case "tool_call":
			if deltas.Len() > 0 {
				t.Fatalf("deltas %q are not closed by a reply before the tool_call", deltas.String())
			}
		}
	}
	if deltas.Len() > 0 {
		t.Fatalf("deltas %q are not closed by a reply", deltas.String())
	}
	return replies
}

// TestReplyEqualsItsDeltas pins the reply record: it is the concatenation of
// the reply_delta records of the same message, reasoning has its own
// records, and every delta sequence is closed before a tool call.
func TestReplyEqualsItsDeltas(t *testing.T) {
	t.Run("reasoning stays out of the reply", func(t *testing.T) {
		useConfigDir(t)
		scriptedProvider(t, "think", func(w http.ResponseWriter, _ *http.Request, _ int32) {
			sse(w, chatReasoning("pondering"), chatDelta("Hello "), chatDelta("there"), chatFinishStop, chatDone)
		})
		got := runProcess(t, "-json -cm gpt-think q hi", nil)
		if replies := assertRepliesCloseTheirDeltas(t, got.stdout); got.status != 0 || !slices.Equal(replies, []string{"Hello there"}) {
			t.Fatalf("got %+v, want one reply equal to its deltas", got)
		}
		if !strings.Contains(got.stdout+got.stderr, `{"type":"reasoning_delta","text":"pondering"}`) {
			t.Fatalf("the reasoning has no record of its own: %+v", got)
		}
	})
	t.Run("a reasoning-only answer has no reply", func(t *testing.T) {
		useConfigDir(t)
		scriptedProvider(t, "think", func(w http.ResponseWriter, _ *http.Request, _ int32) {
			sse(w, chatReasoning("pondering"), chatFinishStop, chatDone)
		})
		got := runProcess(t, "-json -cm gpt-think q hi", nil)
		if replies := assertRepliesCloseTheirDeltas(t, got.stdout); got.status != 0 || len(replies) != 0 {
			t.Fatalf("got %+v, want success and no reply record", got)
		}
	})
	t.Run("text before a tool call is closed before it", func(t *testing.T) {
		useConfigDir(t)
		scriptedProvider(t, "turns", func(w http.ResponseWriter, _ *http.Request, n int32) {
			if n == 1 {
				sse(w, chatReasoning("first thought"), chatDelta("Let me check."), chatToolCall("pwd", "{}"), chatFinishTools, chatDone)
				return
			}
			sse(w, chatDelta("All done."), chatFinishStop, chatDone)
		})
		got := runProcess(t, "-json -cm gpt-turns -t pwd q hi", nil)
		if replies := assertRepliesCloseTheirDeltas(t, got.stdout); got.status != 0 || !slices.Equal(replies, []string{"Let me check.", "All done."}) {
			t.Fatalf("got %+v, want a reply for the text before the call and one for the answer", got)
		}
	})
	t.Run("an empty answer after a tool call has no reply", func(t *testing.T) {
		useConfigDir(t)
		scriptedProvider(t, "turns", func(w http.ResponseWriter, _ *http.Request, n int32) {
			if n == 1 {
				sse(w, chatToolCall("pwd", "{}"), chatFinishTools, chatDone)
				return
			}
			sse(w, chatFinishStop, chatDone)
		})
		got := runProcess(t, "-json -cm gpt-turns -t pwd q hi", nil)
		if replies := assertRepliesCloseTheirDeltas(t, got.stdout); got.status != 0 || len(replies) != 0 {
			t.Fatalf("got %+v, want success and no reply record", got)
		}
	})
}
