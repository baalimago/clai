// Package apikey is the error a model vendor returns when the environment
// variable that holds its API key is unset or empty. It imports nothing
// under internal/, so every vendor package may use it.
package apikey

import "github.com/baalimago/clai/pkg/diagnostic"

// MissingError is a vendor whose API key variable is unset or empty. Its
// text is the vendor's own message; its remedy names the variable, never a
// value.
type MissingError struct {
	// Env is the variable that holds the key.
	Env string
	// Message is the vendor's error text.
	Message string
}

func (e *MissingError) Error() string { return e.Message }

// Remedies names the variable to set. (Remedies.1, F8)
func (e *MissingError) Remedies() []diagnostic.Remedy {
	return []diagnostic.Remedy{{
		Instruction: "Set " + e.Env + " to your API key",
		Command:     []string{"export", e.Env + "=<your key>"},
	}}
}
