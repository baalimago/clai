package internal

import (
	"context"
	"os"
	"testing"

	"github.com/baalimago/clai/internal/photo"
	"github.com/baalimago/clai/internal/text"
)

func TestCreateTextQuerier(t *testing.T) {
	tmp := t.TempDir()
	os.Setenv("OPENAI_API_KEY", "key")
	defer os.Unsetenv("OPENAI_API_KEY")
	conf := text.Configurations{Model: "gpt-4", ConfigDir: tmp}
	q, err := CreateTextQuerier(context.Background(), conf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q == nil {
		t.Fatal("expected querier")
	}
	if _, err := CreateTextQuerier(context.Background(), text.Configurations{Model: "unknown", ConfigDir: tmp}); err == nil {
		t.Error("expected error for unknown model")
	}
}

func TestNewPhotoQuerier(t *testing.T) {
	tmp := t.TempDir()
	os.Setenv("OPENAI_API_KEY", "key")
	defer os.Unsetenv("OPENAI_API_KEY")
	conf := photo.Configurations{Model: "dall-e-3", Output: photo.Output{Type: photo.URL, Dir: tmp}}
	q, err := NewPhotoQuerier(conf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q == nil {
		t.Fatal("expected querier")
	}
}
