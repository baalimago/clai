package main

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
	defaults := flagSet{
		chatModel:     "gpt-4-turbo-preview",
		photoModel:    "dall-e-3",
		picturePrefix: "clai",
		pictureDir:    "picDir",
		stdinReplace:  "stdInReplace",
		printRaw:      false,
		replyMode:     false,
	}
	result := setupFlags(defaults)
	want, got := defaults.chatModel, result.chatModel
	if got != want {
		t.Fatalf("expected: %v, got: %v", want, got)
	}
	want, got = defaults.photoModel, result.photoModel
	if got != want {
		t.Fatalf("expected: %v, got: %v", want, got)
	}
	want, got = defaults.pictureDir, result.pictureDir
	if got != want {
		t.Fatalf("expected: %v, got: %v", want, got)
	}
	want, got = defaults.picturePrefix, result.picturePrefix
	if got != want {
		t.Fatalf("expected: %v, got: %v", want, got)
	}
	want, got = defaults.stdinReplace, result.stdinReplace
	if got != want {
		t.Fatalf("expected: %v, got: %v", want, got)
	}
	wantb, gotb := defaults.printRaw, result.printRaw
	if gotb != wantb {
		t.Fatalf("expected: %v, got: %v", want, got)
	}
	wantb, gotb = defaults.replyMode, result.replyMode
	if gotb != wantb {
		t.Fatalf("expected: %v, got: %v", want, got)
	}
}

// TestSetupFlagsShortFlags tests the setupFlags function with short flags.
func TestSetupFlagsShortFlags(t *testing.T) {
	resetFlags()
	os.Args = []string{"cmd", "-cm", "gpt-4", "-pm", "dall-e-2", "-pd", "/tmp", "-pp", "test-", "-I", "<stdin>", "-r", "-re"}
	defaults := flagSet{}
	result := setupFlags(defaults)

	if result.chatModel != "gpt-4" || result.photoModel != "dall-e-2" || result.pictureDir != "/tmp" ||
		result.picturePrefix != "test-" || result.stdinReplace != "<stdin>" || result.printRaw != true || result.replyMode !=
		true {
		t.Errorf("Unexpected values for short flags, got %+v", result)
	}
}

// TestSetupFlagsLongFlags tests the setupFlags function with long flags.
func TestSetupFlagsLongFlags(t *testing.T) {
	resetFlags()
	os.Args = []string{
		"cmd", "--chat-model", "gpt-4", "--photo-model", "dall-e-2", "--photo-dir", "/tmp", "--photo-prefix",
		"test-", "--replace", "<stdin>", "--raw", "--reply",
	}
	defaults := flagSet{}
	result := setupFlags(defaults)

	if result.chatModel != "gpt-4" || result.photoModel != "dall-e-2" || result.pictureDir != "/tmp" ||
		result.picturePrefix != "test-" || result.stdinReplace != "<stdin>" || result.printRaw != true || result.replyMode !=
		true {
		t.Errorf("Unexpected values for long flags, got %+v", result)
	}
}

// TestSetupFlagsPrecedence tests the precedence of short flags over long flags.
func TestSetupFlagsPrecedence(t *testing.T) {
	resetFlags()
	os.Args = []string{"cmd", "-cm", "gpt-4-short", "-pm", "dall-e-2-short"}
	defaults := flagSet{
		chatModel:  "shouldBeReplaced",
		photoModel: "shouldBeReplaced",
	}
	result := setupFlags(defaults)

	if result.chatModel == defaults.chatModel || result.photoModel == defaults.photoModel {
		t.Errorf("Short flags should have precedence over long flags, got %+v", result)
	}
}
