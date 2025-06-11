package mcp

import "github.com/baalimago/clai/internal/tools"

type ControlEvent struct {
	ServerName string
	Server     tools.McpServer
	InputChan  chan<- any
	OutputChan <-chan any
}
