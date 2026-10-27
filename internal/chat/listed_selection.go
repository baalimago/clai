package chat

import (
	"context"
	"errors"
	"fmt"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// foreignSelection says what selecting a foreign row by its listed index does.
type foreignSelection int

const (
	foreignClone foreignSelection = iota
	foreignRefuse
)

// ErrForeignRowSelected reports that an index named a conversation clai does
// not own, for an action that only applies to clai's own chats.
var ErrForeignRowSelected = errors.New("the selected row is an external conversation")

var errIndexOutOfRange = errors.New("chat index out of range")

// listedChat resolves idx against the rows `chat list` shows by default, so
// the number a user reads there selects the same conversation. A group row
// selects its newest member.
func (cq *ChatHandler) listedChat(ctx context.Context, paginator *ChatIndexPaginator, idx int, foreign foreignSelection) (pub_models.Chat, error) {
	allRows, byName, err := cq.buildChatListRows(ctx, paginator)
	if err != nil {
		return pub_models.Chat{}, fmt.Errorf("failed to build chat list rows: %w", err)
	}
	pr := prepareListRows(allRows, byName, "", nil, true)
	if idx < 0 || idx >= len(pr.rows) {
		return pub_models.Chat{}, fmt.Errorf("%w: %d not in 0..%d", errIndexOutOfRange, idx, len(pr.rows)-1)
	}
	sel := pr.rows[idx]
	if sel.Kind == chatRowGroup {
		members := filterRowsByGroupKey(allRows, sel.GroupKey)
		if len(members) == 0 {
			return pub_models.Chat{}, fmt.Errorf("group %q has no members", sel.GroupKey)
		}
		sel = members[0]
	}
	if sel.Kind == chatRowNative {
		return cq.getByID(sel.ChatID)
	}
	if foreign == foreignRefuse {
		return pub_models.Chat{}, fmt.Errorf("%w: %s session %q", ErrForeignRowSelected, sel.Source, sel.SourceID)
	}
	reader, err := pr.readerFor(sel)
	if err != nil {
		return pub_models.Chat{}, err
	}
	ext, err := cq.readForeignChat(ctx, reader, sel.SourceID)
	if err != nil {
		return pub_models.Chat{}, fmt.Errorf("failed to read %s session %q: %w", sel.Source, sel.SourceID, err)
	}
	return cq.cloneForeignChat(ext)
}
