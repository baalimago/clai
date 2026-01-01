package models

import "io"

type Configurations struct {
	Model         string
	SystemPrompt  string
	ConfigDir     string
	InternalTools []ToolName
	McpServers    []McpServer
	Out           io.Writer
}
