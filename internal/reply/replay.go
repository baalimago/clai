package reply

import (
	"errors"
	"fmt"

	"github.com/baalimago/clai/internal/utils"
)

func Replay(raw bool) error {
	prevReply, err := Load("")
	if err != nil {
		return fmt.Errorf("failed to load previous reply: %v", err)
	}
	amMessages := len(prevReply.Messages)
	if amMessages == 0 {
		return errors.New("failed to find any recent reply")
	}
	mostRecentMsg := prevReply.Messages[amMessages-1]
	utils.AttemptPrettyPrint(mostRecentMsg, "system", raw)
	return nil
}
