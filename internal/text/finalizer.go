package text

import (
	"fmt"
	"time"

	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/models"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/debug"
)

type sessionFinalizer[C models.StreamCompleter] struct {
	querier *Querier[C]
}

func (f sessionFinalizer[C]) Finalize(session *QuerySession) {
	if session == nil || session.Finalized {
		return
	}
	session.Finalized = true
	q := f.querier

	if q.debug {
		ancli.Noticef("post process querier: %+v", q)
	}
	if session.Raw {
		fmt.Fprintln(q.out)
	}

	if session.FinalAssistantText != "" {
		session.Chat.Messages = append(session.Chat.Messages, pub_models.Message{
			Role:    "system",
			Content: session.FinalAssistantText,
		})
	}
	q.chat = session.Chat
	if session.FinalUsage != nil {
		session.Chat.TokenUsage = session.FinalUsage
	}

	if session.ShouldSaveReply {
		if q.costManager != nil {
			timeoutdur := 200 * time.Millisecond
			timeout := time.NewTimer(timeoutdur)
			defer func() {
				if !timeout.Stop() {
					select {
					case <-timeout.C:
					default:
					}
				}
			}()
			select {
			case <-timeout.C:
				ancli.Warnf("skippng wait for cost manager model price fetch after: %v", timeoutdur)
				goto costMgrDone
			case <-q.costMgrRdyChan:
			}
			enrichedChat, err := q.costManager.Enrich(session.Chat)
			if err != nil {
				ancli.PrintErr(fmt.Sprintf("failed to enrich chat with cost estimate: %v\n", err))
			} else {
				session.Chat = enrichedChat
			}
		}
	costMgrDone:
		// Origin stamping is always-on and forward-only: stamp the canonical CWD on
		// first persist, preserve it on every later write (including replies).
		if originErr := chat.EnsureOriginDir(q.configDir, &session.Chat); originErr != nil {
			ancli.Warnf("failed to stamp origin directory: %v\n", originErr)
		}
		q.chat = session.Chat
		err := chat.SaveAsPreviousQuery(q.configDir, session.Chat)
		if err != nil {
			ancli.PrintErr(fmt.Sprintf("failed to save previous query: %v\n", err))
		}
		// History recording is always-on for non-reply queries. A plain -re forks a
		// fresh promoted id, so recording it would pollute the history with
		// near-duplicates; but a directory reply (-dre) continues the bound
		// conversation in place (same id), so recording it just bumps that entry —
		// it keeps the directory's history current and the binding chainable.
		if (!q.replyMode || q.dirReplyMode) && session.Chat.ID != "" && session.Chat.ID != "globalScope" {
			if updateErr := chat.UpdateDirScopeFromCWD(q.configDir, session.Chat.ID); updateErr != nil {
				ancli.Warnf("failed to update directory-scoped binding: %v\n", updateErr)
			}
		}
	}

	if q.debug {
		ancli.PrintOK(fmt.Sprintf("Querier.postProcess:\n%v\n", debug.IndentedJsonFmt(q)))
	}
	if session.FinalAssistantText == "" {
		return
	}
	q.postProcessOutput(pub_models.Message{
		Role:    "system",
		Content: session.FinalAssistantText,
	})
}
