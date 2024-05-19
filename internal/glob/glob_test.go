package glob

import (
	"flag"
	"fmt"
	"os"
	"testing"

	"github.com/baalimago/clai/internal/models"
)

func TestParseGlob(t *testing.T) {
	// Setup a mock filesystem using afero
	tmpDir := t.TempDir()
	os.WriteFile(fmt.Sprintf("%v/%v", tmpDir, "test1.txt"), []byte("content1"), 0o644)
	os.WriteFile(fmt.Sprintf("%v/%v", tmpDir, "test2.txt"), []byte("content2"), 0o644)

	// Test case
	tests := []struct {
		name    string
		glob    string
		want    int // Number of messages expected
		wantErr bool
	}{
		{"two files", fmt.Sprintf("%v/*.txt", tmpDir), 2, false},
		{"no match", "*.log", 0, true},
		{"invalid pattern", "[", 0, true},
		{"home directory", "~/*.txt", 2, false}, // This test case will fail on windows and plan9
	}
	oldHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tmpDir)
	defer func() {
		_ = os.Setenv("HOME", oldHome)
	}()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseGlob(tt.glob)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseGlob() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if len(got) != tt.want {
				t.Errorf("parseGlob() got %v messages, want %v", len(got), tt.want)
			}
		})
	}
}

func TestSetup(t *testing.T) {
	// Set up test cases
	testCases := []struct {
		name        string
		args        []string
		expectedErr bool
	}{
		{
			name:        "Not enough arguments",
			args:        []string{"glob"},
			expectedErr: true,
		},
		{
			name:        "Valid glob",
			args:        []string{"clai", "glob", "*.go", "argument"},
			expectedErr: false,
		},
	}

	// Run test cases
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			flag.Parse()
			_, _, err := Setup("", tc.args)
			if tc.expectedErr && err == nil {
				t.Errorf("Expected an error, but got none")
			}
			if !tc.expectedErr && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
		})
	}
}

func TestCreateChat(t *testing.T) {
	// Set up test case
	glob := "*.go"
	systemPrompt := "You are a helpful assistant."

	// Run the function
	chat, err := CreateChat(glob, systemPrompt)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Check the chat ID
	expectedID := "glob_*.go"
	if chat.ID != expectedID {
		t.Errorf("Expected chat ID: %s, got: %s", expectedID, chat.ID)
	}

	// Check the number of messages
	if len(chat.Messages) < 4 {
		t.Errorf("Expected at least 4 messages, got: %d", len(chat.Messages))
	}
}

func TestConstructGlobMessages(t *testing.T) {
	// Set up test case
	globMessages := []models.Message{
		{Role: "user", Content: "{\"fileName\": \"file1.go\", \"data\": \"package main\"}"},
		{Role: "user", Content: "{\"fileName\": \"file2.go\", \"data\": \"func main()\"}"},
	}

	// Run the function
	messages := constructGlobMessages(globMessages)

	// Check the number of messages
	expectedLen := len(globMessages) + 2
	if len(messages) != expectedLen {
		t.Errorf("Expected %d messages, got: %d", expectedLen, len(messages))
	}

	// Check the system message
	expectedSystemMsg := "You will be given a series of messages each containing contents from files, then a message containing this: '#####'. Using the file content as context, perform the request given in the message after the '#####'."
	if messages[0].Content != expectedSystemMsg {
		t.Errorf("Expected system message: %s, got: %s", expectedSystemMsg, messages[0].Content)
	}

	// Check the user message
	expectedUserMsg := "#####"
	if messages[len(messages)-1].Content != expectedUserMsg {
		t.Errorf("Expected user message: %s, got: %s", expectedUserMsg, messages[len(messages)-1].Content)
	}
}
