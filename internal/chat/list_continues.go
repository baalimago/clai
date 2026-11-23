package chat

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/table"
)

type previousQueryKind int

const (
	prevNone previousQueryKind = iota + 1
	prevNamed
	prevUnreadable
)

type previousQueryState struct {
	Kind   previousQueryKind
	Chat   pub_models.Chat
	Reason string
}

func readPreviousQueryState(ctx context.Context, confDir string) previousQueryState {
	prev, err := LoadPreviousQuery(ctx, confDir)
	switch {
	case err == nil:
		return previousQueryState{Kind: prevNamed, Chat: prev.Chat}
	case errors.Is(err, ErrNoPreviousQuery), errors.Is(err, ErrPreviousQueryGone):
		return previousQueryState{Kind: prevNone}
	default:
		return previousQueryState{Kind: prevUnreadable, Reason: utils.SanitizeTerminalRow(err.Error())}
	}
}

func previousQueryStateFor(ctx context.Context, confDir string) previousQueryState {
	if runenv.From(ctx).JSON() {
		return previousQueryState{Kind: prevNone}
	}
	return readPreviousQueryState(ctx, confDir)
}

func previousQueryLine(state previousQueryState, shown []chatListRow, inGroupView bool) string {
	const prefix = "-re continues: "
	switch state.Kind {
	case prevUnreadable:
		return fmt.Sprintf("%sunreadable record (%s)", prefix, state.Reason)
	case prevNamed:
	default:
		return ""
	}
	id := state.Chat.ID
	for i, r := range shown {
		switch {
		case r.Kind == chatRowNative && r.ChatID == id:
			return fmt.Sprintf("%s%d · %s", prefix, i, labelFor(r.Title, r.FirstUserMessage))
		case r.Kind == chatRowGroup && !inGroupView && slices.Contains(r.GroupMemberIDs, id):
			return fmt.Sprintf("%s%d [group] · %s", prefix, i, previousQueryLabel(state.Chat))
		}
	}
	return fmt.Sprintf("%s%s · %s (not in this view)", prefix, id, previousQueryLabel(state.Chat))
}

func previousQueryLabel(chat pub_models.Chat) string {
	first := ""
	if msg, err := chat.FirstUserMessage(); err == nil {
		first = capIndexPreview(msg.Describe())
	}
	return labelFor(chat.Title, first)
}

func (cq *ChatHandler) writePreviousQueryLine(state previousQueryState, shown []chatListRow, inGroupView bool) error {
	line := previousQueryLine(state, shown, inGroupView)
	if line == "" {
		return nil
	}
	if _, err := fmt.Fprintln(cq.out, strings.TrimRight(line, "\n")); err != nil {
		return diag.Errorf("failed to write the previous query line: %w", err)
	}
	return nil
}

func (cq *ChatHandler) clearPreviousQueryLine(state previousQueryState, shown []chatListRow, inGroupView bool) error {
	line := previousQueryLine(state, shown, inGroupView)
	if cq.input != nil || line == "" {
		return nil
	}
	if err := table.ClearTermTo(cq.out, utils.TerminalRows(line, cq.dims.Width)); err != nil {
		return diag.Errorf("failed to clear the previous query line: %w", err)
	}
	return nil
}
