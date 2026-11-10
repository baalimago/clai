package diagnostic

// Remedy is guidance for a user. Command is an example, not an action.
type Remedy struct {
	Instruction string
	Command     []string
}

// Remedier is an error that knows safe, display-only guidance.
type Remedier interface {
	// Remedies returns the safe actions known for this error. It returns no
	// entries when none are known. It never executes a command. (Remedies.1, F8)
	Remedies() []Remedy
}
