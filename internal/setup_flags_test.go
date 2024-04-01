package internal

import (
	"flag"
	"os"
	"testing"
)

// helper function to reset flags between tests
func resetFlags() {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
}

// TestSetupFlagsDefaultValues tests the setupFlags function with default values.
func TestSetupFlagsDefaultValues(t *testing.T) {
	resetFlags()
	os.Args = []string{"cmd"}
	defaults := Configurations{
		ChatModel:    "gpt-4-turbo-preview",
		PhotoModel:   "dall-e-3",
		PhotoPrefix:  "clai",
		PhotoDir:     "picDir",
		StdinReplace: "stdInReplace",
		PrintRaw:     false,
		ReplyMode:    false,
	}
	result := setupFlags(defaults)
	want, got := defaults.ChatModel, result.ChatModel
	if got != want {
		t.Fatalf("expected: %v, got: %v", want, got)
	}
	want, got = defaults.PhotoModel, result.PhotoModel
	if got != want {
		t.Fatalf("expected: %v, got: %v", want, got)
	}
	want, got = defaults.PhotoDir, result.PhotoDir
	if got != want {
		t.Fatalf("expected: %v, got: %v", want, got)
	}
	want, got = defaults.PhotoPrefix, result.PhotoPrefix
	if got != want {
		t.Fatalf("expected: %v, got: %v", want, got)
	}
	want, got = defaults.StdinReplace, result.StdinReplace
	if got != want {
		t.Fatalf("expected: %v, got: %v", want, got)
	}
	wantb, gotb := defaults.PrintRaw, result.PrintRaw
	if gotb != wantb {
		t.Fatalf("expected: %v, got: %v", want, got)
	}
	wantb, gotb = defaults.ReplyMode, result.ReplyMode
	if gotb != wantb {
		t.Fatalf("expected: %v, got: %v", want, got)
	}
}

// TestSetupFlagsShortFlags tests the setupFlags function with short flags.
func TestSetupFlagsShortFlags(t *testing.T) {
	resetFlags()
	os.Args = []string{"cmd", "-cm", "gpt-4", "-pm", "dall-e-2", "-pd", "/tmp", "-pp", "test-", "-I", "<stdin>", "-r", "-re"}
	defaults := Configurations{}
	result := setupFlags(defaults)

	if result.ChatModel != "gpt-4" || result.PhotoModel != "dall-e-2" || result.PhotoDir != "/tmp" ||
		result.PhotoPrefix != "test-" || result.StdinReplace != "<stdin>" || result.PrintRaw != true || result.ReplyMode !=
		true {
		t.Errorf("Unexpected values for short flags, got %+v", result)
	}
}

// TestSetupFlagsLongFlags tests the setupFlags function with long flags.
func TestSetupFlagsLongFlags(t *testing.T) {
	resetFlags()
	os.Args = []string{
		"cmd", "-chat-model", "gpt-4", "-photo-model", "dall-e-2", "-photo-dir", "/tmp", "-photo-prefix",
		"test-", "-replace", "<stdin>", "-raw", "-reply",
	}
	defaults := Configurations{}
	result := setupFlags(defaults)

	if result.ChatModel != "gpt-4" || result.PhotoModel != "dall-e-2" || result.PhotoDir != "/tmp" ||
		result.PhotoPrefix != "test-" || result.StdinReplace != "<stdin>" || result.PrintRaw != true || result.ReplyMode !=
		true {
		t.Errorf("Unexpected values for long flags, got %+v", result)
	}
}

// TestSetupFlagsPrecedence tests the precedence of short flags over long flags.
func TestSetupFlagsPrecedence(t *testing.T) {
	resetFlags()
	os.Args = []string{"cmd", "-cm", "gpt-4-short", "-pm", "dall-e-2-short"}
	defaults := Configurations{
		ChatModel:  "shouldBeReplaced",
		PhotoModel: "shouldBeReplaced",
	}
	result := setupFlags(defaults)

	if result.ChatModel == defaults.ChatModel || result.PhotoModel == defaults.PhotoModel {
		t.Errorf("Short flags should have precedence over long flags, got %+v", result)
	}
}
