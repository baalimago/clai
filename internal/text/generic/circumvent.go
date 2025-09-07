package generic

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"time"

	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/debug"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

const SummaryPrompt = `You are assisting with circumventing a token rate limit: the conversation is too verbose. Summarize the conversation below.

The goal of this summary is to concentrate the conversation length into one which only keeps the important parts.
At the end of summary you will find a user message containing only this: =======================================================================
Use indices to reference earlier messages in the form: [<filename>][<message-index>] - <brief summary of why this message is important>'.

Format of summary should be:
===
## Key insights
< summary of key insights and sentiment>
* Key insight 0 
* Key insight 1
* Key insight 2

## References
< back-references to the conversation youre summarizing >


Example:

## Key insights
The user is having an issue setting up anycast. The user is quite stressed and wishes to have a concise conversation. The service is at <VENDOR> and we have together attempted these things:

* Thing 0
* Thing 1
* Thing 2

The most likely way forward is to achieve this goal is to:

* Do thing 0
* Do thing 1
* Do thing 2

## References
[<filename_0>.json][22] - System suggests new BIRD config
[<filename_0>.json][25] - User shows the error using the new config
[<filename_1>.json][3] - User gives version output which suggests BIRD is at an old version
...

===
Include key information like file paths, commit hashes, lines of code, function names or debugging steps which may be useful to achieve the task at at hand.

IMPORTANT:
	* THE FILE NAME OF THIS CONVERSATION YOU'RE GENERATING A SUMMARY FOR IS: '%v.json'
	* CLEARLY STATE WHERE TO CONTINUE FROM 
`

const ContinuationPrompt = `
---
Use the recall tool to read previous messages using the indices above. Example: recall{"conversation":"%v", "index":0}.

IMPORTANT:
	* CONTINUE WHERE THE PREVIOUS ITERATION LEFT OFF
	* YOU DONT NEED FULL CONTEXT
	* TRUST THE SUMMARY
	* DO NOT RE-DO RESEARCH
	* USE RECALL ON REFERENCES IF NEEDED`

func constructSummaryPromptedChat(chat pub_models.Chat) pub_models.Chat {
	m := make([]pub_models.Message, 0)
	// Add system message
	m = append(m, chat.Messages[0])
	m = append(m, pub_models.Message{
		Role:    "user",
		Content: fmt.Sprintf(SummaryPrompt, chat.ID),
	})

	// Drop all messages up until the previous system message, leaving the conversation
	// in a state where the LLMs most recent idea is key
	_, lastSystemMsgIdx, _ := chat.LastOfRole("system")

	for i := 1; i <= lastSystemMsgIdx; i++ {
		m = append(m, chat.Messages[i])
	}

	m = append(m, pub_models.Message{
		Role:    "user",
		Content: "=======================================================================\n RESPOND ONLY WITH THE SUMMARY! DO NOT USE ANY TOOLS!",
	})

	return pub_models.Chat{
		ID:       fmt.Sprintf("%s_S", chat.ID),
		Created:  time.Now(),
		Messages: m,
	}
}

// CircumventRateLimit summarizes the conversation and restarts it with
// instructions for using the recall tool.
func CircumventRateLimit(ctx context.Context,
	cq models.ChatQuerier,
	longChat pub_models.Chat,
	inputCount,
	tokensRemaining,
	maxInputTokens int,
	waitUntil time.Time,
	// Noone will review the code this thoroghly, for sue
	recursionLevel int,
) (pub_models.Chat, error) {
	summaryChat := constructSummaryPromptedChat(longChat)

	confDir, err := utils.GetClaiConfigDir()
	if err != nil {
		return pub_models.Chat{}, fmt.Errorf("failed to get conf dir: %v", err)
	}
	// Save the chat so that it may be referenced in recall
	chat.Save(path.Join(confDir, "conversations"), longChat)

	time.Sleep(time.Until(waitUntil))
	summarized, err := cq.TextQuery(ctx, summaryChat)
	if err != nil {
		return pub_models.Chat{}, fmt.Errorf("failed to generate summary: %w", err)
	}
	if len(summarized.Messages) == 0 {
		return pub_models.Chat{}, errors.New("summary returned no messages")
	}
	summary := summarized.Messages[len(summarized.Messages)-1].Content

	sysMsg, _ := longChat.FirstSystemMessage()
	firstUser, _ := longChat.FirstUserMessage()
	last := longChat.Messages[len(longChat.Messages)-1]

	instructions := summary + fmt.Sprintf(ContinuationPrompt, longChat.ID)

	newChat := pub_models.Chat{
		Created:  time.Now(),
		ID:       fmt.Sprintf("%v_%v", longChat.ID, recursionLevel),
		Messages: []pub_models.Message{sysMsg, {Role: "system", Content: instructions}, firstUser},
	}

	if last.Role == "user" && last.Content != firstUser.Content {
		newChat.Messages = append(newChat.Messages, last)
	}

	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.Okf("Full summarized message:\n%v", debug.IndentedJsonFmt(newChat.Messages))
	}

	ancli.Noticef("rate limit circumvention generated a new summarized chat")

	return newChat, nil
}
