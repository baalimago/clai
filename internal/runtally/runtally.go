// Package runtally carries what subagents spend back to the run that started
// them. A clai run that prints a summary, or that was itself started by one,
// opens a Scope: a fresh directory named in EnvDir, which every process it
// starts inherits. A child clai writes its totals, its own children's
// included, there as one JSON file when it ends, and the parent adds them up.
// Nothing is parsed from a child's output, so -reply-only workers keep a
// clean stdout.
package runtally

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/baalimago/clai/internal/quantity"
)

// EnvDir names the directory a child clai reports its totals to.
const EnvDir = "CLAI_RUN_TALLY_DIR"

const (
	reportPattern = "run-*.json"
	pendingSuffix = ".tmp"
)

// Env is the process environment a Scope reads and points children at.
type Env interface {
	Getenv(key string) string
	Setenv(key, value string) error
	Unsetenv(key string) error
}

// OSEnv is the real process environment.
type OSEnv struct{}

func (OSEnv) Getenv(key string) string       { return os.Getenv(key) }
func (OSEnv) Setenv(key, value string) error { return os.Setenv(key, value) }
func (OSEnv) Unsetenv(key string) error      { return os.Unsetenv(key) }

// Totals is what one or more runs spent.
type Totals struct {
	Runs             int     `json:"runs"`
	PromptTokens     int     `json:"prompt_tokens"`
	CachedTokens     int     `json:"cached_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	CostUSD          float64 `json:"cost_usd"`
	// UnpricedRuns counts runs with no cost estimate, e.g. a model with no
	// known price; CostUSD leaves them out.
	UnpricedRuns int `json:"unpriced_runs"`
}

// Add sums two totals.
func (t Totals) Add(o Totals) Totals {
	return Totals{
		Runs:             t.Runs + o.Runs,
		PromptTokens:     t.PromptTokens + o.PromptTokens,
		CachedTokens:     t.CachedTokens + o.CachedTokens,
		CompletionTokens: t.CompletionTokens + o.CompletionTokens,
		CostUSD:          t.CostUSD + o.CostUSD,
		UnpricedRuns:     t.UnpricedRuns + o.UnpricedRuns,
	}
}

// Line spells the totals for a summary row:
// "4 runs · 210k in (90k cached) · 9k out · ~$0.41".
func (t Totals) Line() string {
	runs := strconv.Itoa(t.Runs) + " runs"
	if t.Runs == 1 {
		runs = "1 run"
	}
	in := quantity.Quantity(t.PromptTokens).SI() + " in"
	if t.CachedTokens > 0 {
		in += " (" + quantity.Quantity(t.CachedTokens).SI() + " cached)"
	}
	out := quantity.Quantity(t.CompletionTokens).SI() + " out"
	cost := "cost unknown"
	if t.UnpricedRuns < t.Runs {
		cost = fmt.Sprintf("~$%.2f", t.CostUSD)
		if t.UnpricedRuns > 0 {
			cost += fmt.Sprintf(" (%d unpriced)", t.UnpricedRuns)
		}
	}
	return strings.Join([]string{runs, in, out, cost}, " · ")
}

// Wanted reports whether a run needs a Scope: it prints a summary, or a
// parent waits for its totals.
func Wanted(env Env, summary bool) bool {
	return summary || env.Getenv(EnvDir) != ""
}

// Scope is one run's place in the tree: where it reports, and the
// directory its own children report to.
type Scope struct {
	env       Env
	parentDir string
	childDir  string
}

// Open creates the children's directory under tempRoot and points EnvDir at
// it. Close undoes both.
func Open(env Env, tempRoot string) (*Scope, error) {
	childDir, err := os.MkdirTemp(tempRoot, "clai-run-tally-")
	if err != nil {
		return nil, fmt.Errorf("create subagent tally dir: %w", err)
	}
	s := &Scope{env: env, parentDir: env.Getenv(EnvDir), childDir: childDir}
	if err := env.Setenv(EnvDir, childDir); err != nil {
		return nil, errors.Join(fmt.Errorf("set %s: %w", EnvDir, err), os.RemoveAll(childDir))
	}
	return s, nil
}

// Collect sums the reports the children have written so far. A child still
// running when the run ends is not counted.
func (s *Scope) Collect() (Totals, error) {
	paths, err := filepath.Glob(filepath.Join(s.childDir, reportPattern))
	if err != nil {
		return Totals{}, fmt.Errorf("list subagent reports: %w", err)
	}
	var sum Totals
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			return Totals{}, fmt.Errorf("read subagent report: %w", err)
		}
		var t Totals
		if err := json.Unmarshal(raw, &t); err != nil {
			return Totals{}, fmt.Errorf("parse subagent report %s: %w", filepath.Base(p), err)
		}
		sum = sum.Add(t)
	}
	return sum, nil
}

// ReportOutcome says where a Report went.
type ReportOutcome uint8

const (
	Reported ReportOutcome = iota
	// NoParent: the run was started by no clai that collects totals.
	NoParent
	// ParentGone: the parent ended and removed its directory before this
	// run finished, so there is no one left to count it.
	ParentGone
)

func (o ReportOutcome) String() string {
	switch o {
	case Reported:
		return "reported"
	case NoParent:
		return "no parent"
	case ParentGone:
		return "parent gone"
	default:
		return "unknown"
	}
}

// Report hands t to the parent. The file is written under a temporary name
// and renamed, so Collect never reads half a report.
func (s *Scope) Report(t Totals) (ReportOutcome, error) {
	if s.parentDir == "" {
		return NoParent, nil
	}
	if _, err := os.Stat(s.parentDir); errors.Is(err, fs.ErrNotExist) {
		return ParentGone, nil
	}
	raw, err := json.Marshal(t)
	if err != nil {
		return Reported, fmt.Errorf("encode run tally: %w", err)
	}
	f, err := os.CreateTemp(s.parentDir, reportPattern+pendingSuffix)
	if err != nil {
		return Reported, fmt.Errorf("create run tally: %w", err)
	}
	_, writeErr := f.Write(raw)
	if err := errors.Join(writeErr, f.Close()); err != nil {
		return Reported, errors.Join(fmt.Errorf("write run tally: %w", err), os.Remove(f.Name()))
	}
	if err := os.Rename(f.Name(), strings.TrimSuffix(f.Name(), pendingSuffix)); err != nil {
		return Reported, errors.Join(fmt.Errorf("publish run tally: %w", err), os.Remove(f.Name()))
	}
	return Reported, nil
}

// Close points EnvDir back at the parent and removes the children's
// directory.
func (s *Scope) Close() error {
	var envErr error
	if s.parentDir == "" {
		envErr = s.env.Unsetenv(EnvDir)
	} else {
		envErr = s.env.Setenv(EnvDir, s.parentDir)
	}
	if envErr != nil {
		envErr = fmt.Errorf("restore %s: %w", EnvDir, envErr)
	}
	return errors.Join(envErr, os.RemoveAll(s.childDir))
}
