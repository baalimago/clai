package openrouter

import (
	"strings"
	"testing"
)

func TestParseOpenRouterPrice(t *testing.T) {
	got, err := parseOpenRouterPrice("0.0000016")
	if err != nil {
		t.Fatalf("parseOpenRouterPrice: %v", err)
	}
	if got != 0.0000016 {
		t.Fatalf("unexpected parsed price: %v", got)
	}
}

func TestParseOpenRouterPriceMalformed(t *testing.T) {
	_, err := parseOpenRouterPrice("abc")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "parse price") {
		t.Fatalf("unexpected error: %v", err)
	}
}
