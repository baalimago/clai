package models

import (
	"context"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type mockQuerier struct{}

func (m *mockQuerier) Query(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestQuerier_Context_Test(t *testing.T) {
	// Should pass for a compliant Querier
	Querier_Context_Test(t, &mockQuerier{})
}

type mockChatQuerier struct{}

func (m *mockChatQuerier) Query(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func (m *mockChatQuerier) TextQuery(ctx context.Context, chat pub_models.Chat) (pub_models.Chat, error) {
	<-ctx.Done()
	return pub_models.Chat{}, ctx.Err()
}

func TestChatQuerier_Test(t *testing.T) {
	// Should pass for a compliant ChatQuerier
	ChatQuerier_Test(t, &mockChatQuerier{})
}

type mockStreamCompleter struct{}

func (m *mockStreamCompleter) Setup() error {
	return nil
}

func (m *mockStreamCompleter) StreamCompletions(ctx context.Context, chat pub_models.Chat) (chan CompletionEvent, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestStreamCompleter_Test(t *testing.T) {
	// Should pass for a compliant StreamCompleter
	StreamCompleter_Test(t, &mockStreamCompleter{})
}
