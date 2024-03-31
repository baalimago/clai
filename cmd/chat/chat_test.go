package internal

// func TestGetFirstTokens(t *testing.T) {
// 	tests := []struct {
// 		name     string
// 		prompt   []string
// 		n        int
// 		expected []string
// 	}{
// 		{
// 			name:     "empty prompt",
// 			prompt:   []string{},
// 			n:        5,
// 			expected: []string{},
// 		},
// 		{
// 			name:     "prompt shorter than n",
// 			prompt:   []string{"hello"},
// 			n:        5,
// 			expected: []string{"hello"},
// 		},
// 		{
// 			name:     "prompt exactly n tokens",
// 			prompt:   []string{"how", "are", "you", "doing", "today"},
// 			n:        5,
// 			expected: []string{"how", "are", "you", "doing", "today"},
// 		},
// 		{
// 			name:     "prompt longer than n",
// 			prompt:   []string{"this", "is", "a", "test", "prompt", "with", "more", "tokens"},
// 			n:        5,
// 			expected: []string{"this", "is", "a", "test", "prompt"},
// 		},
// 		{
// 			name:     "prompt with multi-space separation",
// 			prompt:   []string{"this  is", "a", "test"},
// 			n:        3,
// 			expected: []string{"this", "is", "a"},
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			got := getFirstTokens(tt.prompt, tt.n)
// 			if !reflect.DeepEqual(got, tt.expected) {
// 				t.Errorf("getFirstTokens() = %v, expected %v", got, tt.expected)
// 			}
// 		})
// 	}
// }

// func setupTestEnvironment(t *testing.T, chats []Chat) (string, error) {
// 	t.Helper()

// 	tmpHome := t.TempDir()
// 	testDir := filepath.Join(tmpHome, ".clai", "conversations")

// 	err := os.MkdirAll(testDir, 0o755)
// 	if err != nil {
// 		return "", err
// 	}
// 	for _, chat := range chats {
// 		fileName := filepath.Join(testDir, chat.ID+".json")
// 		b, err := json.Marshal(chat)
// 		if err != nil {
// 			return "", err
// 		}
// 		err = os.WriteFile(fileName, b, 0o644)
// 		if err != nil {
// 			return "", err
// 		}
// 	}

// 	return tmpHome, nil
// }

// func TestListChats(t *testing.T) {
// 	chats := []Chat{
// 		{
// 			ID: "chat_1",
// 			Messages: []Message{
// 				{Role: "user", Content: "Hello"},
// 				{Role: "bot", Content: "Hi!"},
// 			},
// 		},
// 		{
// 			ID: "chat_2",
// 			Messages: []Message{
// 				{Role: "user", Content: "Weather?"},
// 				{Role: "bot", Content: "Sunny."},
// 			},
// 		},
// 	}

// 	tmpHome, err := setupTestEnvironment(t, chats)
// 	if err != nil {
// 		t.Fatalf("Failed to setup test environment: %v", err)
// 	}

// 	cq := ChatModelQuerier{home: tmpHome}
// 	listedChats, err := cq.listChats()
// 	if err != nil {
// 		t.Errorf("Expected no error, got %v", err)
// 	}

// 	if len(listedChats) != len(chats) {
// 		t.Errorf("Expected %d chats, got %d", len(chats), len(listedChats))
// 	}

// 	for i, chat := range listedChats {
// 		if chat.ID != chats[i].ID {
// 			t.Errorf("Expected chat ID %s, got %s", chats[i].ID, chat.ID)
// 		}
// 	}
// }

// func TestSaveChat(t *testing.T) {
// 	tempDir, err := setupTestEnvironment(t, nil)
// 	if err != nil {
// 		t.Fatalf("Failed to setup test environment: %v", err)
// 	}
// 	cq := ChatModelQuerier{home: tempDir}
// 	chat := Chat{
// 		ID: "test_chat",
// 		Messages: []Message{
// 			{Role: "user", Content: "Hello"},
// 			{Role: "bot", Content: "Hi there!"},
// 		},
// 	}

// 	err = cq.saveChat(chat)
// 	if err != nil {
// 		t.Fatalf("Failed to save chat: %v", err)
// 	}

// 	savedFilePath := filepath.Join(tempDir, ".clai/conversations", chat.ID+".json")
// 	if _, err := os.Stat(savedFilePath); os.IsNotExist(err) {
// 		t.Fatalf("Chat file was not created: %s", savedFilePath)
// 	}

// 	savedFileContent, err := os.ReadFile(savedFilePath)
// 	if err != nil {
// 		t.Fatalf("Failed to read saved chat file: %v", err)
// 	}

// 	var savedChat Chat
// 	err = json.Unmarshal(savedFileContent, &savedChat)
// 	if err != nil {
// 		t.Fatalf("Failed to unmarshal saved chat: %v", err)
// 	}

// 	if savedChat.ID != chat.ID {
// 		t.Errorf("Expected chat ID %s, got %s", chat.ID, savedChat.ID)
// 	}

// 	if len(savedChat.Messages) != len(chat.Messages) {
// 		t.Errorf("Expected %d messages, got %d", len(chat.Messages), len(savedChat.Messages))
// 	}

// 	for i, msg := range savedChat.Messages {
// 		if msg.Role != chat.Messages[i].Role || msg.Content != chat.Messages[i].Content {
// 			t.Errorf("Expected message %d to be %+v, got %+v", i, chat.Messages[i], msg)
// 		}
// 	}
// }
