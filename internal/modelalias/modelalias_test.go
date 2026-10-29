package modelalias

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// TestResolve_AliasReturnsOrderedCandidates pins the happy path: a configured
// alias resolves to exactly the models it lists, in order.
func TestResolve_AliasReturnsOrderedCandidates(t *testing.T) {
	table := Table{
		"strong": {"claude-sonnet-5-5", "gpt-5.2", "or:deepseek/deepseek-chat"},
	}
	got, err := table.Resolve("strong")
	if err != nil {
		t.Fatalf("Resolve(strong): %v", err)
	}
	want := []string{"claude-sonnet-5-5", "gpt-5.2", "or:deepseek/deepseek-chat"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Resolve(strong) = %v, want %v", got, want)
	}
}

// TestResolve_PlainModelIsItsOwnChain pins the backwards compatibility
// contract: a model the table does not name is tried as itself, alone.
func TestResolve_PlainModelIsItsOwnChain(t *testing.T) {
	table := Table{"strong": {"claude-sonnet-5-5"}}
	got, err := table.Resolve("gpt-5.2")
	if err != nil {
		t.Fatalf("Resolve(gpt-5.2): %v", err)
	}
	want := []string{"gpt-5.2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Resolve(gpt-5.2) = %v, want %v", got, want)
	}
}

// TestResolve_NilAndEmptyTableBehavesLikeNoAliases pins that an unconfigured
// table is not an error: every model resolves to itself.
func TestResolve_NilAndEmptyTableBehavesLikeNoAliases(t *testing.T) {
	for name, table := range map[string]Table{"nil": nil, "empty": {}} {
		t.Run(name, func(t *testing.T) {
			got, err := table.Resolve("gemini-3.6-flash")
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if !reflect.DeepEqual(got, []string{"gemini-3.6-flash"}) {
				t.Fatalf("Resolve = %v, want the model itself", got)
			}
		})
	}
}

// TestResolve_TrimsSurroundingSpace pins that a hand-edited config with a
// stray space still resolves the name the user meant.
func TestResolve_TrimsSurroundingSpace(t *testing.T) {
	table := Table{"strong": {" gpt-5.2 "}}
	got, err := table.Resolve("  strong\n")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"gpt-5.2"}) {
		t.Fatalf("Resolve = %v, want [gpt-5.2]", got)
	}
}

// TestResolve_UntrimmedAliasNameIsNotAnAlias pins that the table keys are
// taken as written: a space inside the key is part of the name.
func TestResolve_UntrimmedAliasNameIsNotAnAlias(t *testing.T) {
	got, err := Table{" strong ": {"gpt-5.2"}}.Resolve("strong")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"strong"}) {
		t.Fatalf("Resolve = %v, want the model itself", got)
	}
}

// TestResolve_RejectsBadEntries pins every way a table entry is unusable, so a
// typo fails loudly instead of silently running one arbitrary model.
func TestResolve_RejectsBadEntries(t *testing.T) {
	testCases := []struct {
		desc    string
		table   Table
		model   string
		problem CandidateProblem
		index   CandidateIndex
	}{
		{
			desc:    "alias lists no models",
			table:   Table{"strong": {}},
			model:   "strong",
			problem: NoCandidates,
			index:   WholeAlias,
		},
		{
			desc:    "alias lists nil models",
			table:   Table{"strong": nil},
			model:   "strong",
			problem: NoCandidates,
			index:   WholeAlias,
		},
		{
			desc:    "blank candidate",
			table:   Table{"strong": {"gpt-5.2", "   "}},
			model:   "strong",
			problem: BlankCandidate,
			index:   1,
		},
		{
			desc:    "alias names itself",
			table:   Table{"strong": {"gpt-5.2", "strong"}},
			model:   "strong",
			problem: SelfCandidate,
			index:   1,
		},
		{
			desc:    "candidate names another alias",
			table:   Table{"strong": {"fast"}, "fast": {"gpt-5.2"}},
			model:   "strong",
			problem: AliasCandidate,
			index:   0,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			got, err := tc.table.Resolve(tc.model)
			if err == nil {
				t.Fatalf("Resolve(%q) = %v, want an error", tc.model, got)
			}
			if !errors.Is(err, ErrInvalidAlias) {
				t.Fatalf("error = %v, want ErrInvalidAlias", err)
			}
			var invalid *InvalidAliasError
			if !errors.As(err, &invalid) {
				t.Fatalf("error = %v, want *InvalidAliasError", err)
			}
			if invalid.Alias != tc.model {
				t.Fatalf("Alias = %q, want %q", invalid.Alias, tc.model)
			}
			if invalid.Problem != tc.problem {
				t.Fatalf("Problem = %q, want %q", invalid.Problem, tc.problem)
			}
			if invalid.Index != tc.index {
				t.Fatalf("Index = %d, want %d", invalid.Index, tc.index)
			}
			if !strings.Contains(err.Error(), tc.model) {
				t.Fatalf("error %q does not name the alias %q", err, tc.model)
			}
		})
	}
}

// TestResolve_RejectsBlankModel pins that an empty configured model is an
// error, not a chain of one empty name.
func TestResolve_RejectsBlankModel(t *testing.T) {
	_, err := Table{"strong": {"gpt-5.2"}}.Resolve("  ")
	if !errors.Is(err, ErrEmptyModel) {
		t.Fatalf("error = %v, want ErrEmptyModel", err)
	}
}
