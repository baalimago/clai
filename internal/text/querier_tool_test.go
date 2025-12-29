package text

import (
	"fmt"
	"testing"

	"github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func Test_maxToolCalls(t *testing.T) {
	amToolCalls := 3
	q := Querier[mockCompleter]{
		maxToolCalls: &amToolCalls,
	}

	getLastMsgStr := func(m []models.Message) string {
		return m[len(q.chat.Messages)-1].Content
	}

	for i := range amToolCalls {
		err := q.doToolCallLogic(models.Call{})
		if err != nil {
			t.Fatalf("failed to handleToolCall: %v", err)
		}

		got := getLastMsgStr(q.chat.Messages)
		want := fmt.Sprintf("Tool calls remaining: %v", amToolCalls-i)
		testboil.AssertStringContains(t, got, want)
	}

	err := q.doToolCallLogic(models.Call{})
	if err != nil {
		t.Fatalf("failed to handleToolCall: %v", err)
	}
	got := getLastMsgStr(q.chat.Messages)
	testboil.AssertStringContains(t, got, "ERROR: No more tool calls allowed")
}
