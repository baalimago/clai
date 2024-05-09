package chat

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

const chatUsage = `clai - (c)omand (l)ine (a)rtificial (i)intelligence

Usage: clai [flags] chat <subcommand> <prompt/chatID>

You may start a new chat using the prompt-modification flags which
are normally used in query mode.

Commands:
  n|new      <prompt>             Create a new chat with the given prompt.
  c|cotinue  <chatID> <prompt>    Continue an existing chat with the given chat ID. Prompt is optional
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

type ChatHandler struct {
	q        models.ChatQuerier
	debug    bool
	username string
	subCmd   string
	chat     models.Chat
	prompt   string
	convDir  string
}

func (q *ChatHandler) Query(ctx context.Context) error {
	return q.actOnSubCmd(ctx)
}

func New(q models.ChatQuerier, confDir, args string) (*ChatHandler, error) {
	username := "user"
	debug := false
	if misc.Truthy(os.Getenv("DEBUG")) {
		debug = true
	}
	argsArr := strings.Split(args, " ")
	subCmd := argsArr[0]
	currentUser, err := user.Current()
	if err == nil {
		username = currentUser.Username
	}

	subPrompt := strings.Join(argsArr[1:], " ")
	return &ChatHandler{
		q:        q,
		username: username,
		debug:    debug,
		subCmd:   subCmd,
		prompt:   subPrompt,
		convDir:  path.Join(confDir, "/.clai/conversations/"),
	}, nil
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
		chats, err := cq.list()
		if err == nil {
			printChats(chats)
		}
		return err
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
	newChat := models.Chat{
		Created: time.Now(),
		ID:      IdFromPrompt(cq.prompt),
		Messages: []models.Message{
			{Role: "user", Content: cq.prompt},
		},
	}
	newChat, err := cq.q.TextQuery(ctx, newChat)
	if err != nil {
		return fmt.Errorf("failed to query chat model: %w", err)
	}
	cq.chat = newChat
	return cq.loop(ctx)
}

func (cq *ChatHandler) findChatByID(potentialChatIdx string) (models.Chat, error) {
	chats, err := cq.list()
	if err != nil {
		return models.Chat{}, fmt.Errorf("failed to list chats: %w", err)
	}
	split := strings.Split(potentialChatIdx, " ")
	firstToken := split[0]
	chatIdx, err := strconv.Atoi(firstToken)
	if err == nil {
		if chatIdx < 0 || chatIdx >= len(chats) {
			return models.Chat{}, fmt.Errorf("chat index out of range")
		}
		// Reassemble the prompt from the split tokens, but without the index
		// selecting the chat
		cq.prompt = strings.Join(split[1:], " ")
		return chats[chatIdx], nil
	} else {
		return cq.getByID(IdFromPrompt(potentialChatIdx))
	}
}

func (cq *ChatHandler) cont(ctx context.Context) error {
	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK(fmt.Sprintf("prompt: %v", cq.prompt))
	}
	chat, err := cq.findChatByID(cq.prompt)
	if err != nil {
		return fmt.Errorf("failed to get chat: %w", err)
	}

	for _, message := range chat.Messages {
		err := utils.AttemptPrettyPrint(message, cq.username, false)
		if err != nil {
			return fmt.Errorf("failed to print chat message: %w", err)
		}
	}
	if cq.prompt != "" {
		chat.Messages = append(chat.Messages, models.Message{Role: "user", Content: cq.prompt})
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

func (cq *ChatHandler) list() ([]models.Chat, error) {
	files, err := os.ReadDir(cq.convDir)
	if err != nil {
		return nil, fmt.Errorf("failed to list conversations: %w", err)
	}
	var chats []models.Chat
	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK(fmt.Sprintf("found '%v' conversations:\n", len(files)))
	}
	for _, file := range files {
		chat, err := FromPath(path.Join(cq.convDir, file.Name()))
		if err != nil {
			return nil, fmt.Errorf("failed to get chat: %w", err)
		}
		chats = append(chats, chat)
	}
	slices.SortFunc(chats, func(a, b models.Chat) int {
		return b.Created.Compare(a.Created)
	})
	return chats, err
}

func printChats(chats []models.Chat) {
	ancli.PrintOK(fmt.Sprintf("found '%v' conversations:\n", len(chats)))
	for i, chat := range chats {
		fmt.Printf("\t%-3s| %s: %v\n", fmt.Sprintf("%v", i), chat.Created.Format("2006-01-02 15:04:05"), chat.ID)
	}
}

func (cq *ChatHandler) getByID(ID string) (models.Chat, error) {
	return FromPath(path.Join(cq.convDir, fmt.Sprintf("%v.json", ID)))
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
			utils.AttemptPrettyPrint(lastMessage, cq.username, false)
		} else {
			fmt.Printf("%v(%v): ", ancli.ColoredMessage(ancli.CYAN, cq.username), "'exit/e/quit/q' to quit")
			var userInput string
			reader := bufio.NewReader(os.Stdin)
			userInput, err := reader.ReadString('\n')
			if err != nil {
				return fmt.Errorf("failed to read user input: %w", err)
			}
			if userInput == "exit\n" || userInput == "quit\n" || userInput == "q\n" || userInput == "e\n" || ctx.Err() != nil {
				return nil
			}
			cq.chat.Messages = append(cq.chat.Messages, models.Message{Role: "user", Content: userInput})
		}

		newChat, err := cq.q.TextQuery(ctx, cq.chat)
		if err != nil {
			return fmt.Errorf("failed to print chat completion: %w", err)
		}
		cq.chat = newChat
	}
}
