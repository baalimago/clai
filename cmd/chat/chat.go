package internal

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

// func (cq *ChatModelQuerier) Chat(ctx context.Context, API_KEY string, subCmd string, prompt []string) error {
// 	switch subCmd {
// 	case "new", "n":
// 		return cq.chatNew(ctx, API_KEY, prompt)
// 	case "continue", "c":
// 		return cq.chatContinue(ctx, API_KEY, prompt)
// 	case "list", "l":
// 		chats, err := cq.listChats()
// 		if err == nil {
// 			printChats(chats)
// 		}
// 		return err
// 	case "delete", "d":
// 		return chatDelete(prompt)
// 	case "query", "q":
// 		// return cq.continueQueryAsChat(ctx, API_KEY, prompt)
// 		return errors.New("not yet implemented")
// 	case "help", "h":
// 		fmt.Print(chatUsage)
// 		return nil
// 	default:
// 		return fmt.Errorf("unknown subcommand: '%s'\n%v", subCmd, chatUsage)
// 	}
// }

// // getFirstTokens returns the first n tokens of the prompt, or the whole prompt if it has less than n tokens
// func getFirstTokens(prompt []string, n int) []string {
// 	ret := make([]string, 0)
// 	for _, word := range prompt {
// 		split := strings.Split(word, " ")
// 		for _, token := range split {
// 			if token == "" {
// 				continue
// 			}
// 			if len(ret) < n {
// 				ret = append(ret, token)
// 			} else {
// 				return ret
// 			}
// 		}
// 	}
// 	return ret
// }

// func (cq *ChatModelQuerier) chatNew(ctx context.Context, API_KEY string, prompt []string) error {
// 	if len(prompt) == 0 {
// 		return errors.New("no prompt provided")
// 	}
// 	messages := cq.constructMessages(prompt)
// 	newMsg, err := cq.StreamCompletions(ctx, API_KEY, messages)
// 	if err != nil {
// 		return fmt.Errorf("failed to query chat model: %w", err)
// 	}
// 	firstTokens := getFirstTokens(prompt, 5)
// 	messages = append(messages, newMsg)
// 	chat := Chat{
// 		ID:       strings.Join(firstTokens, "_"),
// 		Messages: messages,
// 	}

// 	return cq.chatLoop(ctx, API_KEY, chat)
// }

// func (cq *ChatModelQuerier) findChatByID(potentialChatIdx string) (Chat, error) {
// 	chatIdx, err := strconv.Atoi(potentialChatIdx)
// 	if err != nil {
// 		return Chat{}, fmt.Errorf("failed to parse chat index: %w", err)
// 	}
// 	chats, err := cq.listChats()
// 	if err != nil {
// 		return Chat{}, fmt.Errorf("failed to list chats: %w", err)
// 	}
// 	if chatIdx < 0 || chatIdx >= len(chats) {
// 		return Chat{}, fmt.Errorf("chat index out of range")
// 	}
// 	return chats[chatIdx], nil
// }

// func (cq *ChatModelQuerier) chatContinue(ctx context.Context, API_KEY string, prompt []string) error {
// 	var chatOuter Chat
// 	if misc.Truthy(os.Getenv("DEBUG")) {
// 		ancli.PrintOK(fmt.Sprintf("prompt: %v", prompt))
// 	}
// 	if len(prompt) == 1 {
// 		chat, err := cq.findChatByID(prompt[0])
// 		chatOuter = chat
// 		if err != nil {
// 			return fmt.Errorf("failed to find chat by ID: %w", err)
// 		}
// 	} else {
// 		chatID := strings.Join(prompt, "_")
// 		chat, err := getChat(chatID)
// 		chatOuter = chat
// 		if err != nil {
// 			return fmt.Errorf("failed to get chat: %w", err)
// 		}
// 	}

// 	for _, message := range chatOuter.Messages {
// 		err := cq.printChatMessage(message)
// 		if err != nil {
// 			return fmt.Errorf("failed to print chat message: %w", err)
// 		}
// 	}

// 	return cq.chatLoop(ctx, API_KEY, chatOuter)
// }

// func chatDelete(prompt []string) error {
// 	chatID := strings.Join(prompt, " ")
// 	err := deleteChat(chatID)
// 	if err != nil {
// 		return fmt.Errorf("failed to delete chat: %w", err)
// 	}
// 	ancli.PrintOK("chat deleted: " + chatID)
// 	return nil
// }

// func (cq *ChatModelQuerier) listChats() ([]Chat, error) {
// 	convDir := cq.home + "/.clai/conversations"
// 	files, err := os.ReadDir(convDir)
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to list conversations: %w", err)
// 	}
// 	var chats []Chat
// 	if misc.Truthy(os.Getenv("DEBUG")) {
// 		ancli.PrintOK(fmt.Sprintf("found '%v' conversations:\n", len(files)))
// 	}
// 	for _, file := range files {
// 		chat, err := getChatFromPath(convDir + "/" + file.Name())
// 		if err != nil {
// 			return nil, fmt.Errorf("failed to get chat: %w", err)
// 		}
// 		chats = append(chats, chat)
// 	}

// 	if err != nil {
// 		return nil, fmt.Errorf("failed to list chats: %w", err)
// 	}

// 	return chats, err
// }

// func printChats(chats []Chat) {
// 	ancli.PrintOK(fmt.Sprintf("found '%v' conversations:\n", len(chats)))
// 	for i, chat := range chats {
// 		fmt.Printf("\t%v: %v\n", i, chat.ID)
// 	}
// }

// func getChat(chatID string) (Chat, error) {
// 	home, err := os.UserHomeDir()
// 	if err != nil {
// 		return Chat{}, fmt.Errorf("failed to get home dir: %w", err)
// 	}
// 	return getChatFromPath(home + "/.clai/conversations/" + chatID + ".json")
// }

// func deleteChat(chatID string) error {
// 	home, err := os.UserHomeDir()
// 	if err != nil {
// 		return fmt.Errorf("failed to get home dir: %w", err)
// 	}
// 	return os.Remove(home + "/.clai/conversations/" + strings.Replace(chatID, " ", "_", -1) + ".json")
// }

// func (cq *ChatModelQuerier) chatLoop(ctx context.Context, API_KEY string, chat Chat) error {
// 	defer func() {
// 		err := cq.saveChat(chat)
// 		if err != nil {
// 			panic(err)
// 		}
// 	}()
// 	for {
// 		currentUser, err := user.Current()
// 		var username string
// 		if err != nil {
// 			username = "user"
// 		} else {
// 			username = currentUser.Username
// 		}
// 		fmt.Printf("%v: ", ancli.ColoredMessage(ancli.CYAN, username))
// 		var userInput string
// 		reader := bufio.NewReader(os.Stdin)
// 		userInput, err = reader.ReadString('\n')
// 		if err != nil {
// 			return fmt.Errorf("failed to read user input: %w", err)
// 		}
// 		if userInput == "exit\n" || userInput == "quit\n" || ctx.Err() != nil {
// 			return nil
// 		}
// 		chat.Messages = append(chat.Messages, models.Message{Role: "user", Content: strings.TrimRight(userInput, "\n")})
// 		newChatMsg, err := cq.StreamCompletions(ctx, API_KEY, chat.Messages)
// 		if err != nil {
// 			return fmt.Errorf("failed to print chat completion: %w", err)
// 		}
// 		chat.Messages = append(chat.Messages, newChatMsg)
// 		err = cq.saveChat(chat)
// 		if err != nil {
// 			return fmt.Errorf("failed to save chat: %w", err)
// 		}
// 	}
// }
