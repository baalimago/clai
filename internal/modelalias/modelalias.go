package modelalias

import (
	"errors"
	"fmt"
	"strings"
)

// Table maps a verb such as "strong", "fast" or "cheap" to the ordered models
// a run tries in its place. It is the data one mode's config carries under
// "model-aliases"; the retry mechanism in internal/retry iterates the list.
type Table map[string][]string

var (
	// ErrEmptyModel reports a configured model name that is empty.
	ErrEmptyModel = errors.New("model name is empty")
	// ErrInvalidAlias reports a table entry that cannot be used as written.
	ErrInvalidAlias = errors.New("invalid model alias")
)

// CandidateProblem names the one thing wrong with an alias candidate.
type CandidateProblem string

const (
	// NoCandidates reports an alias with an empty model list, which would
	// leave a run with nothing to try.
	NoCandidates CandidateProblem = "lists no models"
	// BlankCandidate reports an empty or all-space entry in the list.
	BlankCandidate CandidateProblem = "is blank"
	// SelfCandidate reports an alias that lists itself.
	SelfCandidate CandidateProblem = "names itself"
	// AliasCandidate reports an alias that lists another alias. Candidates
	// must be concrete models: an alias inside an alias would hide which
	// model is really tried.
	AliasCandidate CandidateProblem = "names another alias"
)

// CandidateIndex is the zero-based position of an invalid candidate in an
// alias's model list, or WholeAlias when the alias list itself is empty.
type CandidateIndex int

// WholeAlias indicates that the problem applies to the alias list as a whole
// rather than to one candidate entry.
const WholeAlias CandidateIndex = -1

// InvalidAliasError reports one unusable alias entry, naming the alias, the
// candidate index (WholeAlias for the list as a whole) and what is wrong.
type InvalidAliasError struct {
	Alias   string
	Index   CandidateIndex
	Problem CandidateProblem
}

func (e *InvalidAliasError) Error() string {
	if e.Index < 0 {
		return fmt.Sprintf("model alias %q %s", e.Alias, e.Problem)
	}
	return fmt.Sprintf("model alias %q candidate %d %s", e.Alias, e.Index, e.Problem)
}

// Unwrap returns ErrInvalidAlias, so a caller can ask the cheap question.
func (e *InvalidAliasError) Unwrap() error { return ErrInvalidAlias }

// Resolve returns the ordered models to try for model: the alias list when the
// table names model, otherwise model itself, which is how a run without aliases
// behaves. A malformed entry is an error and never a silently shortened list.
func (t Table) Resolve(model string) ([]string, error) {
	name := strings.TrimSpace(model)
	if name == "" {
		return nil, ErrEmptyModel
	}
	candidates, isAlias := t[name]
	if !isAlias {
		return []string{name}, nil
	}
	if len(candidates) == 0 {
		return nil, &InvalidAliasError{Alias: name, Index: WholeAlias, Problem: NoCandidates}
	}
	resolved := make([]string, 0, len(candidates))
	for i, candidate := range candidates {
		idx := CandidateIndex(i)
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			return nil, &InvalidAliasError{Alias: name, Index: idx, Problem: BlankCandidate}
		}
		if candidate == name {
			return nil, &InvalidAliasError{Alias: name, Index: idx, Problem: SelfCandidate}
		}
		if _, isAlias := t[candidate]; isAlias {
			return nil, &InvalidAliasError{Alias: name, Index: idx, Problem: AliasCandidate}
		}
		resolved = append(resolved, candidate)
	}
	return resolved, nil
}
