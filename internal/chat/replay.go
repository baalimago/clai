package chat

import (
	"errors"
	"fmt"

	"github.com/baalimago/clai/internal/utils"
)

func Replay(raw bool) error {
	prevReply, err := LoadPrevQuery("")
	if err != nil {
		return fmt.Errorf("failed to load previous reply: %v", err)
	}
	amMessages := len(prevReply.Messages)
	if amMessages == 0 {
		return errors.New("failed to find any recent reply")
	}
	mostRecentMsg := prevReply.Messages[amMessages-1]
	return utils.AttemptPrettyPrint(nil, mostRecentMsg, "system", raw)
}
