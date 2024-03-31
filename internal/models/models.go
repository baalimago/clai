package models

import "context"

type Querier interface {
	Query(ctx context.Context) error
}

type ChatQuerier interface {
	Querier
	TextQuery(context.Context, string) error
	Chat() Chat
	SetChat(Chat)
}

type Chat struct {
	ID       string    `json:"id"`
	Messages []Message `json:"messages"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
