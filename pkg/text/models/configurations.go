package models

type Configurations struct {
	Model         string
	SystemPrompt  string
	ConfigDir     string
	InternalTools []ToolName
	McpServers    []McpServer
}
