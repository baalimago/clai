package tools

import (
	"encoding/json"
	"fmt"
	"slices"
)

type UserFunction struct {
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
	ID       string       `json:"id,omitempty"`
	Name     string       `json:"name,omitempty"`
	Type     string       `json:"type,omitempty"`
	Inputs   Input        `json:"inputs,omitempty"`
	Function UserFunction `json:"function,omitempty"`
}

func (c Call) Json() string {
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

type AiTool interface {
	// Call the AI tool with the given Input. Returns output from the tool or an error
	// if the call returned an error-like. An error-like is either exit code non-zero or
	// restful response non 2xx.
	Call(Input) (string, error)

	// Return the UserFunction, later on used
	// by text queriers to send to their respective
	// models
	UserFunction() UserFunction
}
