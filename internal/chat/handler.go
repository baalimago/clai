package chat

import (
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
	raw         bool
}

func (q *ChatHandler) Query(ctx context.Context) error {
	return q.actOnSubCmd(ctx)
}

func New(q models.ChatQuerier,
	confDir,
	args string,
	preMessages []models.Message,
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
		convDir:     path.Join(claiDir, "conversations"),
		config:      conf,
		raw:         raw,
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
			return cq.listChats(ctx, chats)
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
		return cq.getByID(IDFromPrompt(potentialChatIdx))
	}
}

func (cq *ChatHandler) printChat(chat models.Chat) error {
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
		chat.Messages = append(chat.Messages, models.Message{Role: "user", Content: cq.prompt})
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
		chat, pathErr := FromPath(path.Join(cq.convDir, file.Name()))
		if pathErr != nil {
			return nil, fmt.Errorf("failed to get chat: %w", pathErr)
		}
		chats = append(chats, chat)
	}
	slices.SortFunc(chats, func(a, b models.Chat) int {
		return b.Created.Compare(a.Created)
	})
	return chats, err
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
	fmt.Printf("\t%-3s| %-20s| %v | %v\n", "ID", "Created", "Messages", "Filename + prompt")
	line := strings.Repeat("-", 55)
	fmt.Printf("\t%v\n", line)

	termWidth, err := utils.TermWidth()
	if err != nil {
		return fmt.Errorf("failed to get terminal width: %v", err)
	}
	pageSize := 10
	page := 0
	amChats := len(chats)
	noNumberSelected := true
	selectedNumber := -1
	for noNumberSelected {
		pageIndex := page * pageSize
		listToIndex := pageIndex + pageSize
		if listToIndex > amChats-1 {
			listToIndex = amChats - 1
		}
		for i := pageIndex; i < listToIndex; i++ {
			chat := chats[i]
			chatName := formatChatName(chat.ID)
			fmt.Printf("\t%-3s| %s | %-8v | %v\n",
				fmt.Sprintf("%v", i),
				chat.Created.Format("2006-01-02 15:04:05"),
				len(chat.Messages),
				chatName,
			)

		}
		fmt.Printf("(page: (%v/%v). goto chat: [<num>], next: [<enter>]/[n]ext, [p]rev, [q]uit/[e]it): ", page, amChats/pageSize)
		input, readErr := utils.ReadUserInput()
		if readErr != nil {
			return fmt.Errorf("failed to read input: %w", readErr)
		}
		convNum, atoiErr := strconv.Atoi(input)
		noNumberSelected = atoiErr != nil
		if !noNumberSelected {
			selectedNumber = convNum
		}

		prevers := []string{"prev", "p"}
		if slices.Contains(prevers, input) {
			page -= 1
			if page < 0 {
				page = 0
			}
			// Lets just assume everything but prev is next
		} else {
			if (page+1)*pageSize < amChats {
				page += 1
			}
		}
		utils.ClearTermTo(termWidth, (listToIndex-pageIndex)+1)
	}
	if selectedNumber > len(chats) {
		return fmt.Errorf("selection: '%v' is higher than available chats: '%v'", selectedNumber, len(chats))
	}

	// Table header and some stuff like that
	utils.ClearTermTo(termWidth, 3)
	chat := chats[selectedNumber]
	ancli.Okf("selected conversation with index: '%v', name: '%v', with '%v' messages\n", selectedNumber, chat.ID, len(chat.Messages))
	err = cq.printChat(chat)
	if err != nil {
		return fmt.Errorf("selection ok, print chat not ok: %v", err)
	}
	cq.chat = chat
	return cq.loop(ctx)
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
			utils.AttemptPrettyPrint(lastMessage, cq.username, cq.raw)
		} else {
			fmt.Printf("%v(%v%v): ", ancli.ColoredMessage(ancli.CYAN, cq.username), cq.profileInfo(), " | [q]uit")

			userInput, err := utils.ReadUserInput()
			if err != nil {
				// No context, error should contain context
				return err
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
