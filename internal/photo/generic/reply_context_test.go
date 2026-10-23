package generic

import (
	"strings"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestReplyContext(t *testing.T) {
	t.Run("no messages, no context", func(t *testing.T) {
		got, err := ReplyContext(nil)
		if err != nil || got != "" {
			t.Fatalf("ReplyContext(nil) = %q, %v; want empty", got, err)
		}
	})

	t.Run("text-only messages are byte-identical to the original context", func(t *testing.T) {
		got, err := ReplyContext([]pub_models.Message{{Role: "user", Content: "a fox"}})
		if err != nil {
			t.Fatalf("ReplyContext: %v", err)
		}
		want := "You will be given a serie of messages from different roles, then a prompt descibing what to do with these messages. " +
			"Between the messages and the prompt, there will be this line: '-------------'." +
			"The format is json with the structure {\"role\": \"<role>\", \"content\": \"<content>\"}. " +
			"The roles are 'system' and 'user'. " +
			"Messages:\n[{\"content\":\"a fox\",\"role\":\"user\"}]\n-------------\n"
		if got != want {
			t.Fatalf("ReplyContext =\n%q\nwant\n%q", got, want)
		}
	})

	t.Run("images become their labels and the input is untouched", func(t *testing.T) {
		img := pub_models.ImagePart{MIME: "image/png", Origin: pub_models.OriginUser, Data: []byte("\x89PNG\r\n\x1a\nx")}
		msgs := []pub_models.Message{{Role: "user", Parts: []pub_models.Part{pub_models.TextPart{Text: "look"}, img}}}
		got, err := ReplyContext(msgs)
		if err != nil {
			t.Fatalf("ReplyContext: %v", err)
		}
		if !strings.Contains(got, img.Describe()) || !strings.Contains(got, "look") {
			t.Fatalf("ReplyContext = %q, want the text and %q", got, img.Describe())
		}
		if _, ok := msgs[0].Parts[1].(pub_models.ImagePart); !ok {
			t.Fatal("ReplyContext mutated the caller's parts")
		}
	})
}
