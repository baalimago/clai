package chat

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

const chatUsage = `clai - (c)ommand (l)ine (a)rtificial (i)ntelligence

Usage: clai [flags] chat <subcommand> <prompt/chatID>

You may start a new chat using the prompt-modification flags which
are normally used in query mode.

Commands:
  n|new      <prompt>             Create a new chat with the given prompt.
  c|continue <chatID> <prompt>    Continue an existing chat with the given chat ID. Prompt is optional
  d|delete   <chatID>             Delete the chat with the given chat ID.
  l|list                          List all existing chats.

The chatID is the 5 first words of the prompt joined by underscores. Easiest
way to get the chatID is to list all chats with 'clai chat list'. You may also select
a chat by its index in the list of chats.

The chats are found in %v/.clai/conversations, here they may be manually edited
as JSON files.

Examples:
  - clai chat new "How's the weather?"
  - clai chat list
  - clai chat continue my_chat_id
  - clai chat continue 3
  - clai chat delete my_chat_id
`

const (
	// index | created | messges | prompt
	selectChatTblFormat        = "%-6s| %-20s| %-7v | %v"
	selectChatTblChoicesFormat = "(page: (%v/%v). goto chat: [<num>], next: [<enter>]/[n]ext, [p]rev, [q]uit): "
	actOnChatFormat            = `=== Chat info ===

file path: %v
created_at: %v
am replies:
	user:   '%v'
	tool:   '%v'
	system: '%v'
	assistant: '%v'

%v

(make [p]revQuery (-re/-reply flag), go [b]ack to list, [e]dit messages, [d]elete messages, [c]ontinue conversation, [q]uit): `

	// index | role | length | summary
	editMessageTblFormat        = "%-6v| %-10v| %-7v| %v"
	editMessageChoicesFormat    = `(page: (%v/%v). edit message: [<num>], next: [<enter>]/[n]ext, [p]rev, [q]uit): `
	deleteMessagesChoicesFormat = `(page: (%v/%v). delete message: [<num0>,<num1>,<num2>,...], next: [<enter>]/[n]ext, [p]rev, [q]uit): `
)

type NotCyclicalImport struct {
	UseTools   bool
	UseProfile string
	Model      string
}

type ChatHandler struct {
	q           models.ChatQuerier
	debug       bool
	username    string
	subCmd      string
	chat        pub_models.Chat
	preMessages []pub_models.Message
	prompt      string
	confDir     string
	convDir     string
	config      NotCyclicalImport
	raw         bool
}

func (q *ChatHandler) Query(ctx context.Context) error {
	return q.actOnSubCmd(ctx)
}

func (cq *ChatHandler) actOnSubCmd(ctx context.Context) error {
	if cq.debug {
		ancli.PrintOK(fmt.Sprintf("chat: %+v\n", cq))
	}
	switch cq.subCmd {
	case "new", "n":
		return cq.new(ctx)
	case "continue", "c":
		return cq.cont(ctx)
	case "list", "l":
		return cq.handleListCmd(ctx)
	case "delete", "d":
		return cq.deleteFromPrompt()
	case "query", "q":
		// return cq.continueQueryAsChat(ctx, API_KEY, prompt)
		return errors.New("not yet implemented")
	case "help", "h":
		cfgDir, _ := os.UserConfigDir()
		fmt.Printf(chatUsage, cfgDir)
		return nil
	default:
		return fmt.Errorf("unknown subcommand: '%s'\n%v", cq.subCmd, chatUsage)
	}
}

func (cq *ChatHandler) new(ctx context.Context) error {
	msgs := make([]pub_models.Message, 0)
	msgs = append(msgs, cq.preMessages...)
	msgs = append(msgs, pub_models.Message{Role: "user", Content: cq.prompt})
	newChat := pub_models.Chat{
		Created:  time.Now(),
		ID:       IDFromPrompt(cq.prompt),
		Messages: msgs,
	}
	newChat, err := cq.q.TextQuery(ctx, newChat)
	if err != nil {
		return fmt.Errorf("failed to query chat model: %w", err)
	}
	cq.chat = newChat
	return cq.loop(ctx)
}

func (cq *ChatHandler) findChatByID(potentialChatIdx string) (pub_models.Chat, error) {
	chats, err := cq.list()
	if err != nil {
		return pub_models.Chat{}, fmt.Errorf("failed to list chats: %w", err)
	}
	split := strings.Split(potentialChatIdx, " ")
	firstToken := split[0]
	chatIdx, err := strconv.Atoi(firstToken)
	if err == nil {
		if chatIdx < 0 || chatIdx >= len(chats) {
			return pub_models.Chat{}, fmt.Errorf("chat index out of range")
		}
		// Reassemble the prompt from the split tokens, but without the index
		// selecting the chat
		cq.prompt = strings.Join(split[1:], " ")
		return chats[chatIdx], nil
	} else {
		return cq.getByID(IDFromPrompt(potentialChatIdx))
	}
}

func (cq *ChatHandler) printChat(chat pub_models.Chat) error {
	for _, message := range chat.Messages {
		err := utils.AttemptPrettyPrint(message, cq.username, cq.raw)
		if err != nil {
			return fmt.Errorf("failed to print chat message: %w", err)
		}
	}
	return nil
}

func (cq *ChatHandler) cont(ctx context.Context) error {
	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK(fmt.Sprintf("prompt: %v", cq.prompt))
	}
	chat, err := cq.findChatByID(cq.prompt)
	if err != nil {
		return fmt.Errorf("failed to get chat: %w", err)
	}
	if cq.prompt != "" {
		chat.Messages = append(chat.Messages, pub_models.Message{Role: "user", Content: cq.prompt})
	}
	err = cq.printChat(chat)
	if err != nil {
		return fmt.Errorf("failed to print chat: %v", err)
	}

	cq.chat = chat
	return cq.loop(ctx)
}

func (cq *ChatHandler) deleteFromPrompt() error {
	c, err := cq.findChatByID(cq.prompt)
	if err != nil {
		return fmt.Errorf("failed to get chat to delete: %w", err)
	}
	err = os.Remove(path.Join(cq.convDir, fmt.Sprintf("%v.json", c.ID)))
	if err != nil {
		return fmt.Errorf("failed to delete chat: %w", err)
	}
	ancli.PrintOK(fmt.Sprintf("deleted chat '%v'\n", c.ID))
	return nil
}

func (cq *ChatHandler) getByID(ID string) (pub_models.Chat, error) {
	return FromPath(path.Join(cq.convDir, fmt.Sprintf("%v.json", ID)))
}

func (cq *ChatHandler) profileInfo() string {
	return fmt.Sprintf("tools: '%v', p: '%v', model: '%v'", cq.config.UseTools, cq.config.UseProfile, cq.config.Model)
}

func (cq *ChatHandler) loop(ctx context.Context) error {
	defer func() {
		err := Save(cq.convDir, cq.chat)
		if err != nil {
			panic(err)
		}
	}()

	for {
		lastMessage := cq.chat.Messages[len(cq.chat.Messages)-1]
		if lastMessage.Role == "user" {
			utils.AttemptPrettyPrint(lastMessage, cq.username, cq.raw)
		} else {
			fmt.Printf("%v(%v%v): ", ancli.ColoredMessage(ancli.CYAN, cq.username), cq.profileInfo(), " | [q]uit")

			userInput, err := utils.ReadUserInput()
			if err != nil {
				// No context, error should contain context
				return err
			}
			cq.chat.Messages = append(cq.chat.Messages, pub_models.Message{Role: "user", Content: userInput})
		}

		newChat, err := cq.q.TextQuery(ctx, cq.chat)
		if err != nil {
			return fmt.Errorf("failed to print chat completion: %w", err)
		}
		cq.chat = newChat
	}
}

func New(q models.ChatQuerier,
	confDir,
	args string,
	preMessages []pub_models.Message,
	conf NotCyclicalImport,
	raw bool,
) (*ChatHandler, error) {
	username := "user"
	debug := misc.Truthy(os.Getenv("DEBUG"))
	argsArr := strings.Split(args, " ")
	subCmd := argsArr[0]
	currentUser, err := user.Current()
	if err == nil {
		username = currentUser.Username
	}

	subPrompt := strings.Join(argsArr[1:], " ")
	claiDir, err := utils.GetClaiConfigDir()
	if err != nil {
		return nil, err
	}
	return &ChatHandler{
		q:           q,
		username:    username,
		debug:       debug,
		subCmd:      subCmd,
		prompt:      subPrompt,
		preMessages: preMessages,
		confDir:     claiDir,
		convDir:     path.Join(claiDir, "conversations"),
		config:      conf,
		raw:         raw,
	}, nil
}
