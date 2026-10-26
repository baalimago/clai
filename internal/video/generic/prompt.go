package generic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/imageinput"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

// ErrOneInputImage is returned when the inputs hold more than the one image a video takes.
var ErrOneInputImage = errors.New("video takes at most one input image")

// SetupPrompts builds Prompt from args and stdin. The input image comes from
// -img or from stdin that is wholly an image (raw or base64), never from the
// prompt text; it is passed on as PromptImageB64 (query.7).
func (c *Configurations) SetupPrompts(ctx context.Context, args []string) error {
	prefix := ""
	if c.ReplyMode {
		confDir, err := utils.GetClaiConfigDir()
		if err != nil {
			return fmt.Errorf("get config dir: %w", err)
		}
		iP, err := chat.LoadPrevQuery(confDir)
		if err != nil {
			return fmt.Errorf("load previous query: %w", err)
		}
		prefix, err = replyContext(iP)
		if err != nil {
			return err
		}
	}

	stdin, err := utils.PipedStdin(ctx)
	if err != nil {
		return fmt.Errorf("setup prompt from stdin/args: %w", err)
	}
	collected, err := imageinput.New().Collect(imageinput.CollectRequest{
		Images:       c.Images,
		Stdin:        stdin,
		Args:         args,
		StdinReplace: c.StdinReplace,
	})
	if err != nil {
		return fmt.Errorf("setup prompt from stdin/args: %w", err)
	}
	prompt := collected.Prompt

	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK(fmt.Sprintf("format: '%v', prompt: '%v'\n", c.PromptFormat, prompt))
	}

	switch len(collected.Images) {
	case 0:
	case 1:
		// For image prompts we don't add reply prefixes or formatting.
		c.PromptImageB64 = base64.StdEncoding.EncodeToString(collected.Images[0].Data)
		c.Prompt = prompt
		return nil
	default:
		return fmt.Errorf("%w: got %d", ErrOneInputImage, len(collected.Images))
	}

	if strings.Contains(c.PromptFormat, "%v") {
		prompt = fmt.Sprintf(c.PromptFormat, prompt)
	}
	c.Prompt = prefix + prompt
	return nil
}

// replyContext is today's -re preamble, with every image as its Describe() text.
func replyContext(prev pub_models.Chat) (string, error) {
	if len(prev.Messages) == 0 {
		return "", nil
	}
	replyMessages := "You will be given a serie of messages from different roles, then a prompt descibing what to do with these messages. "
	replyMessages += "Between the messages and the prompt, there will be this line: '-------------'. "
	replyMessages += "The format is json with the structure {\"role\": \"<role>\", \"content\": \"<content>\"}. "
	replyMessages += "The roles are 'system' and 'user'. "
	b, err := json.Marshal(chat.ImagesDescribed(prev.Messages))
	if err != nil {
		return "", fmt.Errorf("encode reply JSON: %w", err)
	}
	return fmt.Sprintf("%vMessages:\n%v\n-------------\n", replyMessages, string(b)), nil
}
