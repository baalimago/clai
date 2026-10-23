package summary

import (
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestRenderTranscript_selectsMessages(t *testing.T) {
	pending := pub_models.ImagePart{MIME: "image/png", Origin: pub_models.OriginUser, Data: []byte("AAAA")}
	handle, err := pending.Handle()
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	chat := pub_models.Chat{Messages: []pub_models.Message{
		{Role: "system", Content: "SYSTEM"},
		{Role: "user", Content: "first question"},
		{Role: "assistant", Content: "early answer"},
		{Role: "assistant", ToolCalls: []pub_models.Call{{Name: "ls"}}},
		{Role: "tool", Content: "TOOL OUTPUT"},
		{Role: "user", Parts: []pub_models.Part{
			pub_models.TextPart{Text: "part one"},
			pending,
			pub_models.TextPart{Text: "part two"},
		}},
		{Role: "assistant", Content: "final answer"},
	}}
	got := renderTranscript(chat)
	body := transcriptBody(t, got)
	want := "user: first question\nuser: part one\n[image " + handle + "]\npart two\nassistant: final answer"
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

func TestRenderLoopPhaseTranscript_usesGoalAndHandover(t *testing.T) {
	chat := pub_models.Chat{Messages: []pub_models.Message{
		{Role: "system", Content: "SYSTEM"},
		{Role: "user", Content: "ship the feature"},
		{Role: "assistant", Content: "early answer"},
		{Role: "user", Content: "Continue working toward the goal: ship the feature"},
		{Role: "assistant", Content: "Added parsing and tests.\n\nRemaining: docs."},
	}}
	got := renderLoopPhaseTranscript(chat)
	body := transcriptBody(t, got)
	want := "goal: ship the feature\n\nprevious iteration handover: Added parsing and tests.\n\nRemaining: docs."
	if body != want {
		t.Fatalf("body = %q, want %q", body, want)
	}
	for _, forbidden := range []string{"SYSTEM", "early answer", "Continue working"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("loop-phase transcript must not contain %q:\n%s", forbidden, got)
		}
	}
	if !strings.Contains(got, "previous iteration achieved") || !strings.Contains(got, strconv.Itoa(LoopPhaseSummaryMaxRunes)) {
		t.Fatalf("instruction must describe progression and the loop-phase limit:\n%s", got)
	}
}

func TestRenderLoopPhaseTranscript_capsGoalWithoutLosingHandover(t *testing.T) {
	chat := pub_models.Chat{Messages: []pub_models.Message{
		{Role: "user", Content: "goal " + strings.Repeat("g", loopGoalInputRunes*2)},
		{Role: "assistant", Content: "achieved X"},
	}}
	got := renderLoopPhaseTranscript(chat)
	if !strings.Contains(got, "previous iteration handover: achieved X") {
		t.Fatalf("the handover must survive a long goal:\n%s", got)
	}
	if n := utf8.RuneCountInString(strings.SplitN(strings.SplitN(got, "goal: ", 2)[1], "\n\nprevious iteration handover", 2)[0]); n > loopGoalInputRunes {
		t.Fatalf("goal rune count = %d, want at most %d", n, loopGoalInputRunes)
	}
}

func TestRenderLoopPhaseTranscript_placeholders(t *testing.T) {
	body := transcriptBody(t, renderLoopPhaseTranscript(pub_models.Chat{}))
	if !strings.Contains(body, "goal: (not recorded)") || !strings.Contains(body, "handover: (no previous iteration)") {
		t.Fatalf("empty chat body = %q, want both placeholders", body)
	}
}

func TestRenderLoopPlanTranscript_usesGoalAndAsksForTheApproach(t *testing.T) {
	chat := pub_models.Chat{Messages: []pub_models.Message{
		{Role: "system", Content: "SYSTEM"},
		{Role: "user", Content: "ship the feature"},
	}}
	got := renderLoopPlanTranscript(chat)
	body := transcriptBody(t, got)
	if want := "goal: ship the feature"; body != want {
		t.Fatalf("body = %q, want %q", body, want)
	}
	for _, forbidden := range []string{"SYSTEM", "previous iteration"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("loop-plan transcript must not contain %q:\n%s", forbidden, got)
		}
	}
	if !strings.Contains(got, "approach") || !strings.Contains(got, strconv.Itoa(LoopPhaseSummaryMaxRunes)) {
		t.Fatalf("instruction must ask for the approach and name the loop-phase limit:\n%s", got)
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
