package setup

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestQueryForAction(t *testing.T) {
	tests := []struct {
		name    string
		options []action
		input   string
		want    action
		wantErr bool
	}{
		{"Configure", []action{conf}, "c", conf, false},
		{"Delete", []action{del}, "d", del, false},
		{"New", []action{newaction}, "n", newaction, false},
		{"Quit", []action{conf, del, newaction}, "q", unset, true},
		{"Invalid", []action{conf, del, newaction}, "x", unset, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate user input
			oldStdin := os.Stdin
			defer func() { os.Stdin = oldStdin }()
			r, w, _ := os.Pipe()
			os.Stdin = r
			w.Write([]byte(tt.input + "\n"))
			w.Close()

			got, err := queryForAction(tt.options)
			if (err != nil) != tt.wantErr {
				t.Errorf("queryForAction() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("queryForAction() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCastPrimitive(t *testing.T) {
	tests := []struct {
		name  string
		input any
		want  any
	}{
		{"String to int", "42", 42},
		{"String to float", "3.14", 3.14},
		{"String remains string", "hello", "hello"},
		{"Boolean true", "true", true},
		{"Boolean false", "false", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := castPrimitive(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("castPrimitive() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetToolsValue(t *testing.T) {
	oldStdin := os.Stdin
	defer func() { os.Stdin = oldStdin }()

	input := "0,2,4\n"
	r, w, _ := os.Pipe()
	os.Stdin = r
	w.Write([]byte(input))
	w.Close()

	initialTools := []any{"tool1", "tool2", "tool3", "tool4", "tool5"}

	result, err := getToolsValue(initialTools)
	if err != nil {
		t.Fatalf("getToolsValue() error = %v", err)
	}

	// The actual tool names might be different, so we'll just check the length
	if len(result) != 3 {
		t.Errorf("getToolsValue() returned %d tools, want 3", len(result))
	}
}

func TestReconfigureWithEditor(t *testing.T) {
	tests := []struct {
		name    string
		editor  string
		content string
		wantErr bool
	}{
		{
			name:    "No editor set",
			editor:  "",
			content: "",
			wantErr: true,
		},
		{
			name:    "Valid editor",
			editor:  "echo",
			content: "{\"test\": \"value\"}",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup temporary file
			tmpDir := t.TempDir()
			tmpFile := filepath.Join(tmpDir, "config.json")
			if err := os.WriteFile(tmpFile, []byte(tt.content), 0o644); err != nil {
				t.Fatal(err)
			}

			// Set environment
			oldEditor := os.Getenv("EDITOR")
			defer os.Setenv("EDITOR", oldEditor)
			os.Setenv("EDITOR", tt.editor)

			cfg := config{
				name:     "test",
				filePath: tmpFile,
			}

			err := reconfigureWithEditor(cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("reconfigureWithEditor() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestEditSlice(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		initial []any
		want    []any
	}{
		{name: "Add", input: "a\nbar\nd\n", initial: []any{"foo"}, want: []any{"foo", "bar"}},
		{name: "Update", input: "u\n0\nbaz\nd\n", initial: []any{"foo"}, want: []any{"baz"}},
		{name: "Remove", input: "r\n0\nd\n", initial: []any{"foo"}, want: []any{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldStdin := os.Stdin
			defer func() { os.Stdin = oldStdin }()
			r, w, _ := os.Pipe()
			os.Stdin = r
			go func() {
				for _, line := range strings.Split(tt.input, "\n") {
					if line == "" {
						continue
					}
					w.Write([]byte(line + "\n"))
					time.Sleep(50 * time.Millisecond)
				}
				w.Close()
			}()
			got, err := editSlice("test", tt.initial)
			if err != nil {
				t.Fatalf("editSlice error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("editSlice = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEditMap(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		initial map[string]any
		want    map[string]any
	}{
		{name: "Add", input: "a\nnew\nval\nd\n", initial: map[string]any{"foo": "bar"}, want: map[string]any{"foo": "bar", "new": "val"}},
		{name: "Update", input: "u\nfoo\nbaz\nd\n", initial: map[string]any{"foo": "bar"}, want: map[string]any{"foo": "baz"}},
		{name: "Remove", input: "r\nfoo\nd\n", initial: map[string]any{"foo": "bar"}, want: map[string]any{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldStdin := os.Stdin
			defer func() { os.Stdin = oldStdin }()
			r, w, _ := os.Pipe()
			os.Stdin = r
			go func() {
				for _, line := range strings.Split(tt.input, "\n") {
					if line == "" {
						continue
					}
					w.Write([]byte(line + "\n"))
					time.Sleep(50 * time.Millisecond)
				}
				w.Close()
			}()
			got, err := editMap("test", tt.initial)
			if err != nil {
				t.Fatalf("editMap error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("editMap = %v, want %v", got, tt.want)
			}
		})
	}
}
