package summary

import (
	"strings"
	"testing"
	"unicode/utf8"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestRenderTranscript_selectsMessages(t *testing.T) {
	chat := pub_models.Chat{Messages: []pub_models.Message{
		{Role: "system", Content: "SYSTEM"},
		{Role: "user", Content: "first question"},
		{Role: "assistant", Content: "early answer"},
		{Role: "assistant", ToolCalls: []pub_models.Call{{Name: "ls"}}},
		{Role: "tool", Content: "TOOL OUTPUT"},
		{Role: "user", ContentParts: []pub_models.ImageOrTextInput{
			{Type: "text", Text: "part one"},
			{Type: "image_url", ImageB64: &pub_models.ImageURL{URL: "data:image/png;base64,AAAA"}},
			{Type: "text", Text: "part two"},
		}},
		{Role: "assistant", Content: "final answer"},
	}}
	got := renderTranscript(chat)
	body := transcriptBody(t, got)
	want := "user: first question\nuser: part one part two\nassistant: final answer"
	if body != want {
		t.Fatalf("body = %q, want %q", body, want)
	}
	for _, forbidden := range []string{"SYSTEM", "TOOL OUTPUT", "early answer", "AAAA"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("rendered transcript must not contain %q:\n%s", forbidden, got)
		}
	}
	if !strings.Contains(got, ToolName) || !strings.Contains(got, "60") || !strings.Contains(got, "240") {
		t.Fatalf("instruction must name the tool and both limits:\n%s", got)
	}

	empty := renderTranscript(pub_models.Chat{Messages: []pub_models.Message{{Role: "system", Content: "only"}}})
	if body := transcriptBody(t, empty); body != "" {
		t.Fatalf("chat without user messages must render an empty block, got %q", body)
	}
}

func TestRenderTranscript_capsInput(t *testing.T) {
	head := "HEAD-"
	long := head + strings.Repeat("é", InputMaxRunes*2)
	got := renderTranscript(pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: long}}})
	body := transcriptBody(t, got)
	if n := utf8.RuneCountInString(body); n != InputMaxRunes {
		t.Fatalf("body rune count = %d, want exactly %d", n, InputMaxRunes)
	}
	if !strings.HasPrefix(body, "user: "+head) {
		t.Fatalf("cap must preserve the head, got %q", body[:20])
	}
	if !strings.HasSuffix(body, ellipsis) {
		t.Fatalf("truncated body must end with the ellipsis marker, got %q", body[len(body)-8:])
	}

	short := renderTranscript(pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "tiny"}}})
	if strings.Contains(short, ellipsis) {
		t.Fatalf("untruncated transcript must carry no marker: %q", short)
	}
}

func transcriptBody(t *testing.T, rendered string) string {
	t.Helper()
	open, close := "<transcript>\n", "\n</transcript>"
	start := strings.Index(rendered, open)
	end := strings.Index(rendered, close)
	if start != 0 || end < 0 {
		t.Fatalf("rendered transcript lacks the block tags:\n%s", rendered)
	}
	return rendered[len(open):end]
}
