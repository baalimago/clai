package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/user"
	"strings"

	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/num"
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

type Chat struct {
	ID       string    `json:"id"`
	Messages []Message `json:"messages"`
}

func (cq *chatModelQuerier) chat(ctx context.Context, API_KEY string, subCmd string, prompt []string) error {
	switch subCmd {
	case "n":
		fallthrough
	case "new":
		return cq.chatNew(ctx, API_KEY, prompt)
	case "c":
		fallthrough
	case "continue":
		return cq.chatContinue(ctx, API_KEY, prompt)
	case "l":
		fallthrough
	case "list":
		return chatList()
	case "d":
		fallthrough
	case "delete":
		return chatDelete(prompt)
	case "q":
		fallthrough
	case "query":
		// return cq.continueQueryAsChat(ctx, API_KEY, prompt)
		return errors.New("not yet implemented")
	case "h":
		fallthrough
	case "help":
		fmt.Print(chatUsage)
		return nil
	default:
		return fmt.Errorf("unknown subcommand: '%s'\n%v", subCmd, chatUsage)
	}
}

func (cq *chatModelQuerier) chatNew(ctx context.Context, API_KEY string, prompt []string) error {
	if len(prompt) == 0 {
		return errors.New("no prompt provided")
	}
	messages := cq.constructMessages(prompt)
	initialCompletion, err := cq.queryChatModel(ctx, API_KEY, messages)
	if err != nil {
		return fmt.Errorf("failed to query chat model: %w", err)
	}
	err = cq.printChatCompletion(initialCompletion)
	if err != nil {
		return fmt.Errorf("failed to print chat completion: %w", err)
	}

	lenPrompt := len(prompt)
	capped := num.Cap(lenPrompt, 1, 5)
	shortenedPrompt := prompt[:capped]
	messages = append(messages, initialCompletion.Choices[0].Message)
	chat := Chat{
		ID:       strings.Join(shortenedPrompt, "_"),
		Messages: messages,
	}

	return cq.chatLoop(ctx, API_KEY, chat)
}

func (cq *chatModelQuerier) chatContinue(ctx context.Context, API_KEY string, prompt []string) error {
	chatID := strings.Join(prompt, "_")
	chat, err := getChat(chatID)
	if err != nil {
		return fmt.Errorf("failed to get chat: %w", err)
	}
	for _, message := range chat.Messages {
		err = cq.printChatMessage(message)
		if err != nil {
			return fmt.Errorf("failed to print chat message: %w", err)
		}
	}

	return cq.chatLoop(ctx, API_KEY, chat)
}

func chatList() error {
	chats, err := listChats()
	if err != nil {
		return fmt.Errorf("failed to list chats: %w", err)
	}
	ancli.PrintOK(fmt.Sprintf("found '%v' conversations:\n", len(chats)))
	for i, chat := range chats {
		fmt.Printf("\t%v: %v\n", i, chat.ID)
	}
	return nil
}

func chatDelete(prompt []string) error {
	chatID := strings.Join(prompt, " ")
	err := deleteChat(chatID)
	if err != nil {
		return fmt.Errorf("failed to delete chat: %w", err)
	}
	ancli.PrintOK("chat deleted: " + chatID)
	return nil
}

func listChats() ([]Chat, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home dir: %w", err)
	}
	convDir := home + "/.clai/conversations"

	files, err := os.ReadDir(convDir)
	if err != nil {
		return nil, fmt.Errorf("failed to list conversations: %w", err)
	}
	var ret []Chat
	if os.Getenv("DEBUG") == "true" {
		ancli.PrintOK(fmt.Sprintf("found '%v' conversations:\n", len(files)))
	}
	for _, file := range files {
		chat, err := getChatFromPath(convDir + "/" + file.Name())
		if err != nil {
			return nil, fmt.Errorf("failed to get chat: %w", err)
		}
		ret = append(ret, chat)
	}

	return ret, nil
}

func getChat(chatID string) (Chat, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Chat{}, fmt.Errorf("failed to get home dir: %w", err)
	}
	return getChatFromPath(home + "/.clai/conversations/" + chatID + ".json")
}

func getChatFromPath(path string) (Chat, error) {
	if os.Getenv("DEBUG") == "true" {
		ancli.PrintOK(fmt.Sprintf("Reading chat from '%v'\n", path))
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return Chat{}, fmt.Errorf("failed to read file: %w", err)
	}
	var chat Chat
	err = json.Unmarshal(b, &chat)
	if err != nil {
		return Chat{}, fmt.Errorf("failed to decode JSON: %w", err)
	}

	return chat, nil
}

func deleteChat(chatID string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home dir: %w", err)
	}
	return os.Remove(home + "/.clai/conversations/" + strings.Replace(chatID, " ", "_", -1) + ".json")
}

func (cq *chatModelQuerier) chatLoop(ctx context.Context, API_KEY string, chat Chat) error {
	defer func() {
		err := saveChat(chat)
		if err != nil {
			panic(err)
		}
	}()
	for {
		currentUser, err := user.Current()
		var username string
		if err != nil {
			username = "user"
		} else {
			username = currentUser.Username
		}
		fmt.Printf("%v: ", ancli.ColoredMessage(ancli.CYAN, username))
		var userInput string
		reader := bufio.NewReader(os.Stdin)
		userInput, err = reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("failed to read user input: %w", err)
		}
		if userInput == "exit\n" || userInput == "quit\n" || ctx.Err() != nil {
			return nil
		}
		chat.Messages = append(chat.Messages, Message{Role: "user", Content: strings.TrimRight(userInput, "\n")})
		chatCompletion, err := cq.queryChatModel(ctx, API_KEY, chat.Messages)
		if err != nil {
			return fmt.Errorf("failed to query chat model: %w", err)
		}
		err = cq.printChatCompletion(chatCompletion)
		if err != nil {
			return fmt.Errorf("failed to print chat completion: %w", err)
		}
		chat.Messages = append(chat.Messages, chatCompletion.Choices[0].Message)
		err = saveChat(chat)
		if err != nil {
			return fmt.Errorf("failed to save chat: %w", err)
		}
	}
}

func saveChat(chat Chat) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home dir: %w", err)
	}
	b, err := json.Marshal(chat)
	if err != nil {
		return fmt.Errorf("failed to encode JSON: %w", err)
	}
	return os.WriteFile(home+"/.clai/conversations/"+chat.ID+".json", b, 0o644)
}
