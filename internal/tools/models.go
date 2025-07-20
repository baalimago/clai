package tools

import pub_models "github.com/baalimago/clai/pkg/text/models"

type LLMTool interface {
	// Call the LLM tool with the given Input. Returns output from the tool or an error
	// if the call returned an error-like. An error-like is either exit code non-zero or
	// http response which isn't 2xx or 3xx.
	Call(pub_models.Input) (string, error)

	// Return the Specification, later on used
	// by text queriers to send to their respective
	// models
	Specification() pub_models.Specification
}

type McpServerConfig map[string]pub_models.McpServer
