package chat

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path"
	"strconv"
	"strings"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

const chatUsage = `clai - (c)omand (l)ine (a)rtificial (i)intelligence 

chat usage:

Commands:                                                                                                         
  chat n [prompt]                   Create a new chat with the given prompt.                                      
  chat new [prompt]                 (Alias of the above)                                                          
  chat c [chatID]                   Continue an existing chat with the given chat ID.                             
  chat continue [chatID]            (Alias of the above)                                                          
  chat l                            List all existing chats.                                                      
  chat list                         (Alias of the above)                                                          
  chat d [chatID]                   Delete the chat with the given chat ID.                                       
  chat delete [chatID]              (Alias of the above)                                                          
  chat q [prompt]                   (Not yet implemented) Query an existing chat with the given prompt.           

The chatID is the 5 first words of the prompt joined by underscores. Easiest
way to get the chatID is to list all chats with 'clai chat list'.

You can also manually edit each message in the chats in ~/.clai/conversations.

Examples:                                                                                                         
  - Create a new chat:                                                                                            
    clai chat new "How's the weather?"                                                                          
  - Continue an existing chat by ID:                                                                              
    clai chat continue my_chat_id                                                                               
  - List all chats:                                                                                               
    clai chat list                                                                                              
  - Delete a chat by ID:                                                                                          
    clai chat delete my_chat_id`

type ChatQuerier struct {
	q        models.ChatQuerier
	debug    bool
	username string
	subCmd   string
	chatID   string
	prompt   string
	convDir  string
}

func (q *ChatQuerier) Query(ctx context.Context) error {
	return q.chat(ctx)
}

func New(q models.ChatQuerier, args string) (*ChatQuerier, error) {
	home := os.Getenv("HOME")
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
	return &ChatQuerier{
		q:        q,
		username: username,
		debug:    debug,
		subCmd:   subCmd,
		chatID:   IdFromPrompt(subPrompt),
		prompt:   subPrompt,
		convDir:  path.Join(home, "/.clai/conversations/"),
	}, nil
}

func (cq *ChatQuerier) chat(ctx context.Context) error {
	if cq.debug {
		ancli.PrintOK(fmt.Sprintf("chat: %+v\n", cq))
	}
	switch cq.subCmd {
	case "new", "n":
		return cq.chatNew(ctx)
	case "continue", "c":
		return cq.chatContinue(ctx)
	case "list", "l":
		chats, err := cq.listChats()
		if err == nil {
			printChats(chats)
		}
		return err
	case "delete", "d":
		return cq.chatDelete()
	case "query", "q":
		// return cq.continueQueryAsChat(ctx, API_KEY, prompt)
		return errors.New("not yet implemented")
	case "help", "h":
		fmt.Print(chatUsage)
		return nil
	default:
		return fmt.Errorf("unknown subcommand: '%s'\n%v", cq.subCmd, chatUsage)
	}
}

func (cq *ChatQuerier) chatNew(ctx context.Context) error {
	err := cq.q.TextQuery(ctx, cq.prompt)
	if err != nil {
		return fmt.Errorf("failed to query chat model: %w", err)
	}
	// Update ID so that the subcommand isn't included. Slightly ugly, but it works.
	cNew := cq.q.Chat()
	cNew.ID = IdFromPrompt(cq.prompt)
	cq.q.SetChat(cNew)
	return cq.chatLoop(ctx)
}

func (cq *ChatQuerier) findChatByID(potentialChatIdx string) (models.Chat, error) {
	chats, err := cq.listChats()
	if err != nil {
		return models.Chat{}, fmt.Errorf("failed to list chats: %w", err)
	}
	chatIdx, err := strconv.Atoi(potentialChatIdx)
	if err == nil {
		if chatIdx < 0 || chatIdx >= len(chats) {
			return models.Chat{}, fmt.Errorf("chat index out of range")
		}
		return chats[chatIdx], nil
	} else {
		return cq.getChat(IdFromPrompt(potentialChatIdx))
	}
}

func (cq *ChatQuerier) chatContinue(ctx context.Context) error {
	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK(fmt.Sprintf("prompt: %v", cq.prompt))
	}
	chat, err := cq.findChatByID(cq.chatID)
	if err != nil {
		return fmt.Errorf("failed to get chat: %w", err)
	}

	for _, message := range chat.Messages {
		err := tools.AttemptPrettyPrint(message, cq.username)
		if err != nil {
			return fmt.Errorf("failed to print chat message: %w", err)
		}
	}
	cq.q.SetChat(chat)
	return cq.chatLoop(ctx)
}

func (cq *ChatQuerier) chatDelete() error {
	return os.Remove(path.Join(cq.convDir, cq.chatID, ".json"))
}

func (cq *ChatQuerier) listChats() ([]models.Chat, error) {
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

	if err != nil {
		return nil, fmt.Errorf("failed to list chats: %w", err)
	}

	return chats, err
}

func printChats(chats []models.Chat) {
	ancli.PrintOK(fmt.Sprintf("found '%v' conversations:\n", len(chats)))
	for i, chat := range chats {
		fmt.Printf("\t%v: %v\n", i, chat.ID)
	}
}

func (cq *ChatQuerier) getChat(chatID string) (models.Chat, error) {
	return FromPath(path.Join(cq.convDir, fmt.Sprintf("%v.json", chatID)))
}

func (cq *ChatQuerier) chatLoop(ctx context.Context) error {
	defer func() {
		err := Save(cq.convDir, cq.q.Chat())
		if err != nil {
			panic(err)
		}
	}()
	for {
		fmt.Printf("%v(%v): ", ancli.ColoredMessage(ancli.CYAN, cq.username), "'exit' or 'quit' to quit")
		var userInput string
		reader := bufio.NewReader(os.Stdin)
		userInput, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("failed to read user input: %w", err)
		}
		if userInput == "exit\n" || userInput == "quit\n" || ctx.Err() != nil {
			return nil
		}
		err = cq.q.TextQuery(ctx, userInput)
		if err != nil {
			return fmt.Errorf("failed to print chat completion: %w", err)
		}
	}
}
