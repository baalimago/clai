package tools

import (
	"encoding/json"
	"fmt"
	"slices"
)

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

type Input map[string]any

type Call struct {
	ID       string        `json:"id,omitempty"`
	Name     string        `json:"name,omitempty"`
	Type     string        `json:"type,omitempty"`
	Inputs   Input         `json:"inputs,omitempty"`
	Function Specification `json:"function,omitempty"`
}

// PrettyPrint the call, showing name and what input params is used
// on a concise way
func (c Call) PrettyPrint() string {
	paramStr := ""
	i := 0
	lenInp := len(c.Inputs)
	for flag, val := range c.Inputs {
		paramStr += fmt.Sprintf("'%v': '%v'", flag, val)
		if i < lenInp-1 {
			paramStr += ","
		}
		i++
	}

	return fmt.Sprintf("Call: '%s', inputs: [ %s ]", c.Name, paramStr)
}

func (c Call) JSON() string {
	json, err := json.MarshalIndent(c, "", " ")
	if err != nil {
		return fmt.Sprintf("ERROR: Failed to unmarshal: %v", err)
	}
	return string(json)
}

type ParameterObject struct {
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Enum        []string `json:"enum,omitempty"`
}

type ValidationError struct {
	fieldsMissing []string
}

func NewValidationError(fieldsMissing []string) error {
	// Sort for deterministic error print
	slices.Sort(fieldsMissing)
	return ValidationError{fieldsMissing: fieldsMissing}
}

func (v ValidationError) Error() string {
	return fmt.Sprintf("validation error, fields missing: %v", v.fieldsMissing)
}

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
