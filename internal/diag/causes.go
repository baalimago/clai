package diag

import (
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/baalimago/clai/pkg/claierr"
	"github.com/baalimago/clai/pkg/diagnostic"
)

// cause is one independent incident of a terminal error.
type cause struct {
	message   string
	operation string
	location  diagnostic.SourceLocation
	stack     []diagnostic.SourceLocation
	remedies  []string
	api       *claierr.APIError
}

// errorContext is the nearest error context on a path: the deepest Locator,
// and the deepest one that captured a stack.
type errorContext struct {
	locator diagnostic.Locator
	stacked diagnostic.Locator
}

// independentCauses splits err at each errors.Join into its independent
// causes. A joined error that another branch already matches, such as a
// classification sentinel, is no separate incident, and branches that share
// one claierr API response are one incident. Each remedy appears once. A
// message loses the trailing newlines some error texts carry, such as
// go_away_boilerplate's usage errors.
func independentCauses(err error) []cause {
	branches := branchCauses(err, nil, errorContext{})
	for i := range branches {
		branches[i].message = strings.TrimRight(branches[i].message, "\n")
	}
	causes := mergeIncidents(branches)
	seen := map[string]bool{}
	for i := range causes {
		remedies := causes[i].remedies[:0]
		for _, remedy := range causes[i].remedies {
			if !seen[remedy] {
				seen[remedy] = true
				remedies = append(remedies, remedy)
			}
		}
		causes[i].remedies = remedies
	}
	return causes
}

func branchCauses(branch, parent error, inherited errorContext) []cause {
	found := cause{message: branch.Error()}
	context := inherited
	for current := branch; current != nil; {
		collectFacts(current, &found, &context)
		switch unwrapper := current.(type) {
		case interface{ Unwrap() error }:
			parent, current = current, unwrapper.Unwrap()
		case interface{ Unwrap() []error }:
			children := unwrapper.Unwrap()
			if !isJoin(current, parent, children) {
				for _, child := range children {
					mergeFacts(&found, &context, branchCauses(child, current, errorContext{}))
				}
				current = nil
				continue
			}
			children = distinct(children)
			if len(children) == 1 {
				found.message = strings.Replace(found.message, current.Error(), children[0].Error(), 1)
				parent, current = current, children[0]
				continue
			}
			var causes []cause
			for _, child := range children {
				for _, childCause := range branchCauses(child, current, context) {
					childCause.remedies = append(slices.Clone(found.remedies), childCause.remedies...)
					causes = append(causes, childCause)
				}
			}
			return causes
		default:
			current = nil
		}
	}
	applyContext(&found, context)
	return []cause{found}
}

func collectFacts(err error, found *cause, context *errorContext) {
	if locator, ok := err.(diagnostic.Locator); ok {
		context.locator = locator
		if len(locator.Stack()) > 0 {
			context.stacked = locator
		}
	}
	if remedier, ok := err.(diagnostic.Remedier); ok {
		for _, remedy := range remedier.Remedies() {
			found.remedies = append(found.remedies, remedyText(remedy))
		}
	}
	if apiErrorer, ok := err.(claierr.APIErrorer); ok && found.api == nil {
		found.api = apiErrorer.API()
	}
}

// mergeFacts folds the causes of one non-joined multi-cause error, such as
// one wrapping several %w operands, into found.
func mergeFacts(found *cause, context *errorContext, children []cause) {
	for _, child := range children {
		found.remedies = append(found.remedies, child.remedies...)
		if found.api == nil {
			found.api = child.api
		}
		if context.locator == nil && found.location == (diagnostic.SourceLocation{}) && child.location != (diagnostic.SourceLocation{}) {
			found.operation, found.location = child.operation, child.location
		}
		if context.stacked == nil && len(found.stack) == 0 {
			found.stack = child.stack
		}
	}
}

func applyContext(found *cause, context errorContext) {
	if context.locator != nil {
		found.operation = context.locator.Operation()
		found.location = context.locator.Location()
	}
	if context.stacked != nil {
		found.stack = context.stacked.Stack()
	}
}

// isJoin reports whether node is an errors.Join of children rather than one
// error wrapping several operands: its text is theirs joined by newlines,
// and a parent that wraps it shows that text whole.
func isJoin(node, parent error, children []error) bool {
	texts := make([]string, 0, len(children))
	for _, child := range children {
		if child != nil {
			texts = append(texts, child.Error())
		}
	}
	text := node.Error()
	if text != strings.Join(texts, "\n") {
		return false
	}
	return parent == nil || strings.Contains(parent.Error(), text)
}

// distinct drops each child that another child already matches with
// errors.Is, keeping the first of mutually matching children.
func distinct(children []error) []error {
	kept := make([]error, 0, len(children))
	for i, child := range children {
		if child == nil {
			continue
		}
		redundant := false
		for j, other := range children {
			if i == j || other == nil || !errors.Is(other, child) {
				continue
			}
			if j < i || !errors.Is(child, other) {
				redundant = true
				break
			}
		}
		if !redundant {
			kept = append(kept, child)
		}
	}
	return kept
}

// mergeIncidents joins causes that share one claierr API response and drops
// repeated causes.
func mergeIncidents(causes []cause) []cause {
	merged := make([]cause, 0, len(causes))
	for _, next := range causes {
		index := slices.IndexFunc(merged, func(existing cause) bool {
			sameResponse := existing.api != nil && existing.api == next.api
			return sameResponse || (existing.message == next.message && existing.location == next.location)
		})
		if index < 0 {
			merged = append(merged, next)
			continue
		}
		existing := &merged[index]
		if existing.message != next.message {
			existing.message += "; " + next.message
		}
		existing.remedies = append(existing.remedies, next.remedies...)
	}
	return merged
}

func remedyText(remedy diagnostic.Remedy) string {
	if len(remedy.Command) == 0 {
		return remedy.Instruction
	}
	return remedy.Instruction + " (example: " + strings.Join(remedy.Command, " ") + ")"
}

func locationText(location diagnostic.SourceLocation) string {
	if location.File == "" {
		return ""
	}
	return location.File + ":" + strconv.Itoa(location.Line)
}

func stackFrameText(frame diagnostic.SourceLocation) string {
	return locationText(frame) + " " + frame.Function
}

func causeMessages(causes []cause) string {
	messages := make([]string, len(causes))
	for i, found := range causes {
		messages[i] = found.message
	}
	return strings.Join(messages, "\n")
}
