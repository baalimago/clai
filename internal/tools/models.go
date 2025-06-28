package tools

import (
	"encoding/json"
	"fmt"
)

type LLMTool interface {
	// Call the LLM tool with the given Input. Returns output from the tool or an error
	// if the call returned an error-like. An error-like is either exit code non-zero or
	// http response which isn't 2xx or 3xx.
	Call(Input) (string, error)

	// Return the Specification, later on used
	// by text queriers to send to their respective
	// models
	Specification() Specification
}

type Specification struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Format is the same, but name of the field different. So this way, each
	// vendor can set their own field name
	Inputs *InputSchema `json:"input_schema,omitempty"`
	// Chatgpt wants this
	Arguments string `json:"arguments,omitempty"`
}

type InputSchema struct {
	Type       string                     `json:"type"`
	Required   []string                   `json:"required"`
	Properties map[string]ParameterObject `json:"properties"`
}

// Patch the input schema, making it compatible with mcp server
// everything https://github.com/modelcontextprotocol/servers/tree/main/src/everything
// which I'm calibrating towards
func (is *InputSchema) Patch() {
	if is.Required == nil {
		is.Required = make([]string, 0)
	}
	if is.Properties == nil {
		is.Properties = make(map[string]ParameterObject)
	}
	if is.Type == "" {
		is.Type = "object"
	}
}

// IsOk checks if the input schema is ok
func (is *InputSchema) IsOk() bool {
	for _, p := range is.Properties {
		if p.Type == "array" && p.Enum == nil {
			return false
		}
	}
	return true
}

type Input map[string]any

type Call struct {
	ID       string        `json:"id,omitempty"`
	Name     string        `json:"name,omitempty"`
	Type     string        `json:"type,omitempty"`
	Inputs   *Input        `json:"inputs,omitempty"`
	Function Specification `json:"function,omitempty"`
}

// Patch the call, filling structs and initializing fields so that
// all vendors become as happy as they can be, padding initialization
// inconsistencies
func (c *Call) Patch() {
	if c.Type == "" {
		c.Type = "function"
	}
	if c.Function.Name == "" {
		if c.Name == "" {
			c.Name = "EMPTY-STRING"
		}
		c.Function.Name = c.Name
	}
	if c.Function.Inputs != nil {
		c.Function.Inputs.Patch()
	}
	if c.Function.Arguments == "" {
		c.Function.Arguments = c.JSON()
	}
}

// PrettyPrint the call, showing name and what input params is used
// on a concise way
func (c Call) PrettyPrint() string {
	paramStr := ""
	i := 0
	var inp Input
	if c.Inputs != nil {
		inp = *c.Inputs
	}
	lenInp := len(inp)
	for flag, val := range inp {
		paramStr += fmt.Sprintf("'%v': '%v'", flag, val)
		if i < lenInp-1 {
			paramStr += ","
		}
		i++
	}

	return fmt.Sprintf("Call: '%s', inputs: [ %s ]", c.Name, paramStr)
}

func (c Call) JSON() string {
	json, err := json.Marshal(c)
	if err != nil {
		return fmt.Sprintf("ERROR: Failed to unmarshal: %v", err)
	}
	return string(json)
}

type ParameterObject struct {
	Type        string    `json:"type"`
	Description string    `json:"description"`
	Enum        *[]string `json:"enum,omitempty"`
}

type McpServerConfig map[string]McpServer

type McpServer struct {
	Name    string            `json:"-"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}
