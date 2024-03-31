package models

import "context"

type Querier interface {
	Query(ctx context.Context) error
}

type Chat struct {
	ID       string    `json:"id"`
	Messages []Message `json:"messages"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
