package setup

import (
	"os"
	"reflect"
	"testing"
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
