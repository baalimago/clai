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
	"github.com/baalimago/go_away_boilerplate/pkg/num"
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
	chat        models.Chat
	preMessages []models.Message
	prompt      string
	convDir     string
	config      NotCyclicalImport
}

func (q *ChatHandler) Query(ctx context.Context) error {
	return q.actOnSubCmd(ctx)
}

func New(q models.ChatQuerier, confDir, args string, preMessages []models.Message, conf NotCyclicalImport) (*ChatHandler, error) {
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
		q:           q,
		username:    username,
		debug:       debug,
		subCmd:      subCmd,
		prompt:      subPrompt,
		preMessages: preMessages,
		convDir:     path.Join(confDir, "/.clai/conversations/"),
		config:      conf,
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
			cq.listChats(ctx, chats)
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
	msgs := make([]models.Message, 0)
	msgs = append(msgs, cq.preMessages...)
	msgs = append(msgs, models.Message{Role: "user", Content: cq.prompt})
	newChat := models.Chat{
		Created:  time.Now(),
		ID:       IdFromPrompt(cq.prompt),
		Messages: msgs,
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

func (cq *ChatHandler) printChat(chat models.Chat) error {
	for _, message := range chat.Messages {
		err := utils.AttemptPrettyPrint(message, cq.username, false)
		if err != nil {
			return fmt.Errorf("failed to print chat message: %w", err)
		}
	}
	if cq.prompt != "" {
		chat.Messages = append(chat.Messages, models.Message{Role: "user", Content: cq.prompt})
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

func readUserInput() (string, error) {
	reader := bufio.NewReader(os.Stdin)
	input, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("failed to read user input: %w", err)
	}
	return strings.TrimSpace(input), nil
}

func formatChatName(chatName string) string {
	chatNameLen := len(chatName)
	amCharsToPrint := num.Cap(chatNameLen, 0, 25)
	overflow := chatNameLen > amCharsToPrint
	chatName = chatName[:amCharsToPrint]
	if overflow {
		chatName += "..."
	}
	return strings.ReplaceAll(chatName, "\n", "\\n")
}

func (cq *ChatHandler) listChats(ctx context.Context, chats []models.Chat) error {
	ancli.PrintOK(fmt.Sprintf("found '%v' conversations:\n", len(chats)))
	fmt.Printf("\t%-3s| %-20s| %v\n", "ID", "Created", "Filename + prompt")
	line := strings.Repeat("-", 55)
	fmt.Printf("\t%v\n", line)
	itemsPerPage := 10
	amChats := len(chats)
	for i, chat := range chats {
		chatName := formatChatName(chat.ID)
		fmt.Printf("\t%-3s| %s | %v\n",
			fmt.Sprintf("%v", i),
			chat.Created.Format("2006-01-02 15:04:05"),
			chatName,
		)
		if (i+1)%itemsPerPage == 0 && i != 1 {
			fmt.Printf("(page: [%v/%v]. goto chat: <num>, continue: <enter>, q/quit/e/exit: <quit>): ", i, amChats)
			input, err := readUserInput()
			if err != nil {
				return fmt.Errorf("failed to read input: %v", err)
			}
			if input == "q" || input == "quit" || input == "e" || input == "exit" {
				return nil
			}
			if input != "\n" && input != "" && input != "c" {
				convNum, atoiErr := strconv.Atoi(input)
				if atoiErr != nil {
					return fmt.Errorf("not a number, now what..? maybe break. yeah let's break. Use numbers or enter..! error: %v", err)
				}
				chat := chats[convNum]
				err = cq.printChat(chat)
				if err != nil {
					return fmt.Errorf("selection ok, print chat not ok: %v", err)
				}
				cq.chat = chat
				return cq.loop(ctx)
			}
			termWidth, err := utils.TermWidth()
			if err != nil {
				return fmt.Errorf("failed to get terminal width: %v", err)
			}
			utils.ClearTermTo(termWidth, itemsPerPage+1)
		}
	}
	return nil
}

func (cq *ChatHandler) getByID(ID string) (models.Chat, error) {
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
			utils.AttemptPrettyPrint(lastMessage, cq.username, false)
		} else {
			fmt.Printf("%v(%v%v): ", ancli.ColoredMessage(ancli.CYAN, cq.username), cq.profileInfo(), " | type exit/e/quit/q to quit")
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
