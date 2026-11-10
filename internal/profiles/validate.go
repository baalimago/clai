package profiles

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/profile"
	"github.com/baalimago/clai/internal/remote"
)

// ExitStatus is the status a profiles subcommand asks Run for.
type ExitStatus int

const (
	// ExitClean is no error finding on any route.
	ExitClean ExitStatus = 0
	// ExitFindings is at least one route's refusal.
	ExitFindings ExitStatus = 1
	// ExitUsage is a command line that named no target.
	ExitUsage ExitStatus = 2
)

const validateHelp = `profiles validate <target> [-json]. Checks a profile body the way every route
would and prints one line per finding, severity member: reason [routes], where
[routes] is * when all three refuse it. The target is a path, the name of a
profile in <config-dir>/profiles, or a cchub:, gh: or https:// target, which is
fetched without being trusted. Exits 0 when no route has reason to refuse the
body, 1 when one has, and 2 on a usage error. It never runs the profile.

Examples:
  clai profiles validate ./reviewer.json
  clai profiles validate reviewer
  clai profiles validate cchub:lorentz/trade-bot@3
  clai profiles validate ./reviewer.json -json`

const schemaHelp = `profiles schema. Prints the profile JSON Schema document this clai ships, byte for
byte, so it can be committed beside the docs and served by the Hub.

Examples:
  clai profiles schema
  clai profiles schema > schema/profile-1.0.json`

// Validate checks target's body the way every route would and prints every
// finding, warnings included. It never runs the profile and never records
// trust for a remote target.
func (d CommandDeps) Validate(ctx context.Context, target string, asJSON bool) (ExitStatus, error) {
	d = d.withEnv(ctx)
	if strings.TrimSpace(target) == "" {
		return ExitUsage, nil
	}
	confDir, err := d.lookupConfigDir(ctx)
	if err != nil {
		return ExitClean, err
	}
	body, route, err := d.readTarget(ctx, confDir, target)
	if err != nil {
		return ExitClean, err
	}
	env, err := d.profileEnv(confDir)
	if err != nil {
		return ExitClean, err
	}
	findings, err := profile.Validate(body, route, env)
	if err != nil {
		return ExitClean, diag.Errorf("check %s: %w", target, err)
	}
	if err := d.printFindings(findings, asJSON); err != nil {
		return ExitClean, err
	}
	if findings.Failed(profile.File) || findings.Failed(profile.Remote) || findings.Failed(profile.Publish) {
		return ExitFindings, nil
	}
	return ExitClean, nil
}

// readTarget reads the body a target names: a remote target is fetched
// without recording trust, a path is read, and a bare name is read from the
// config dir's profiles directory.
func (d CommandDeps) readTarget(ctx context.Context, confDir, target string) ([]byte, profile.Route, error) {
	if remote.IsRemote(target) {
		r, err := d.resolver(ctx, confDir)
		if err != nil {
			return nil, profile.Remote, err
		}
		body, err := r.Peek(ctx, target)
		if err != nil {
			return nil, profile.Remote, &targetError{err}
		}
		return body, profile.Remote, nil
	}
	path := profilePath(confDir, target)
	body, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, profile.File, fmt.Errorf("no such profile: %s", target)
	}
	if err != nil {
		return nil, profile.File, diag.Errorf("read profile %s: %w", target, err)
	}
	return body, profile.File, nil
}

// targetError is a target validate could not read. It drops the status a
// run's refusal exits with (3), because validate answers 1 for every target
// it cannot check and 0 or 1 for the findings themselves.
type targetError struct{ err error }

func (e *targetError) Error() string { return e.err.Error() }

// profilePath is where a target that is no remote target lives: the path as
// written when it names one, otherwise <confDir>/profiles/<name>.json.
func profilePath(confDir, target string) string {
	if strings.ContainsRune(target, os.PathSeparator) || strings.HasSuffix(target, ".json") {
		return target
	}
	return filepath.Join(confDir, "profiles", target+".json")
}

// printFindings writes one line per finding, or the findings as one JSON
// array and nothing else.
func (d CommandDeps) printFindings(findings profile.Findings, asJSON bool) error {
	out := d.out()
	if asJSON {
		encoded, err := json.Marshal(jsonFindings(findings))
		if err != nil {
			return diag.Errorf("encode findings: %w", err)
		}
		if _, err := fmt.Fprintf(out, "%s\n", encoded); err != nil {
			return diag.Errorf("print findings: %w", err)
		}
		return nil
	}
	for _, finding := range findings {
		if _, err := fmt.Fprintln(out, findingLine(finding)); err != nil {
			return diag.Errorf("print findings: %w", err)
		}
	}
	return nil
}

// jsonFinding is a finding as `-json` prints it.
type jsonFinding struct {
	Severity string   `json:"severity"`
	Member   string   `json:"member"`
	Reason   string   `json:"reason"`
	Routes   []string `json:"routes"`
	Fixed    string   `json:"fixed,omitempty"`
	Unread   bool     `json:"unread,omitempty"`
}

// jsonFindings is the findings array, never null.
func jsonFindings(findings profile.Findings) []jsonFinding {
	out := make([]jsonFinding, 0, len(findings))
	for _, finding := range findings {
		out = append(out, jsonFinding{
			Severity: severityName(finding.Severity),
			Member:   finding.Member,
			Reason:   finding.Reason,
			Routes:   jsonRoutes(finding.Routes),
			Fixed:    finding.Fixed,
			Unread:   finding.Unread,
		})
	}
	return out
}

// jsonRoutes names every route a finding refuses; no route tag refuses all
// three, which the line form prints as `*`.
func jsonRoutes(routes []profile.Route) []string {
	if len(routes) == 0 {
		return []string{"file", "remote", "publish"}
	}
	return routeNames(routes)
}

// findingLine renders one finding: its severity, the member, what is wrong,
// and the routes that refuse it.
func findingLine(finding profile.Finding) string {
	routes := "*"
	if names := routeNames(finding.Routes); len(names) > 0 {
		routes = strings.Join(names, " ")
	}
	return fmt.Sprintf("%s %s: %s [%s]", severityName(finding.Severity), finding.Member, finding.Reason, routes)
}

// routeNames names the routes a finding is tagged with; no tag names all
// three and is left to the caller to render.
func routeNames(routes []profile.Route) []string {
	names := make([]string, 0, len(routes))
	for _, route := range routes {
		names = append(names, routeName(route))
	}
	return names
}

func routeName(route profile.Route) string {
	switch route {
	case profile.File:
		return "file"
	case profile.Remote:
		return "remote"
	case profile.Publish:
		return "publish"
	}
	return "unknown"
}

func severityName(severity profile.Severity) string {
	if severity == profile.Warning {
		return "warning"
	}
	return "error"
}

// out is where the subcommand prints; withEnv fills it on every entry.
func (d CommandDeps) out() io.Writer {
	if d.Out == nil {
		return io.Discard
	}
	return d.Out
}

// validateCommand builds `profiles validate <target> [-json]`.
func validateCommand(deps CommandDeps) *internal.Command {
	var asJSON bool
	sub := &internal.Command{
		Name:     "validate",
		Desc:     "Check a profile body and print what a run or publish would refuse",
		HelpText: validateHelp,
		Register: func(fs *flag.FlagSet) {
			fs.BoolVar(&asJSON, "json", false, "print the findings as one JSON array and nothing else")
		},
	}
	sub.OnRun = func(ctx context.Context, c *internal.Command) error {
		deps := deps.withEnv(ctx)
		args := c.Args()[1:]
		if len(args) == 0 {
			return deps.validateUsage("")
		}
		fs := c.Flagset()
		fs.SetOutput(io.Discard)
		if err := fs.Parse(args[1:]); err != nil {
			return deps.validateUsage(err.Error())
		}
		if rest := fs.Args(); len(rest) > 0 {
			return deps.validateUsage(fmt.Sprintf("unexpected argument %q: validate takes one target", rest[0]))
		}
		status, err := deps.Validate(ctx, args[0], asJSON)
		if err != nil {
			return err
		}
		deps.setExit(status)
		return nil
	}
	return sub
}

// validateUsage prints one usage line, naming the offending argument when
// there is one, and asks for exit 2.
func (d CommandDeps) validateUsage(problem string) error {
	line := "usage: clai profiles validate <target> [-json]"
	if problem != "" {
		line += ": " + problem
	}
	if _, err := fmt.Fprintln(d.out(), line); err != nil {
		return diag.Errorf("print usage: %w", err)
	}
	d.setExit(ExitUsage)
	return nil
}

// schemaCommand builds `profiles schema`.
func schemaCommand(deps CommandDeps) *internal.Command {
	return &internal.Command{
		Name:     "schema",
		Desc:     "Print the profile JSON Schema document this clai ships",
		HelpText: schemaHelp,
		OnRun: func(ctx context.Context, _ *internal.Command) error {
			if _, err := deps.withEnv(ctx).out().Write(profile.Schema()); err != nil {
				return diag.Errorf("print the profile schema: %w", err)
			}
			return nil
		},
	}
}
