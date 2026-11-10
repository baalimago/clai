package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/clai/internal/cloud/cloudcmd"
	"github.com/baalimago/clai/internal/confdir"
	"github.com/baalimago/clai/internal/configschema"
	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/profile"
	"github.com/baalimago/clai/internal/profiles"
	"github.com/baalimago/clai/internal/setup"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/cmd"
)

// configSynopsis is the usage text `clai config` and every usage error under
// it print.
const configSynopsis = `clai config [verb] [kind] [target]

  edit                                  the interactive store editor (today's setup)
  list [kind]                           the store's files
  schema [kind]                         no kind: the kinds; a kind: its document
  validate <kind> <target> [-json]      findings for one target
  publish [kind] [name] [mode]          publish to the Hub
  trust profile <target>                trust a remote target
  install profile <target> [name]       install a remote target
  update profile [name]                 update installed profiles
  dir [subpath]                         the config dir or a registered subpath`

const configHelp = `config. The config store.

` + configSynopsis + `

Examples:
  clai config edit
  clai config list profile
  clai config schema theme
  clai config validate profile ./reviewer.json
  clai config publish profile reviewer -note 'tighter stop-loss'
  clai config dir
  clai -n config edit 0 0 q   # macro-drive the wizard, then quit`

// profileKind names the profile document, which lives in package profile
// rather than the configschema corpus.
const profileKind = "profile"

// configDeps carries the collaborators the validate, publish and
// profile-only verbs need from the composition root. The four store verbs use
// none of them.
type configDeps struct {
	profiles profiles.CommandDeps
	publish  func(ctx context.Context, args []string, opts cloudcmd.PublishOptions) error
	rerun    func(args []string) int
	setExit  func(int)
}

// configCommand builds the `config` tree: the store editor, the file listing,
// the schema documents, the validator, the Hub publisher, the profile-only
// verbs and the side-effect-free config-dir lookup.
func configCommand(deps configDeps) *internal.Command {
	setExit := deps.setExit
	c := &internal.Command{
		Name:     "config",
		Desc:     "The config store: edit, list, schema, validate, publish and dir",
		HelpText: configHelp,
	}
	c.OnRun = func(_ context.Context, c *internal.Command) error {
		args := c.Args()
		if len(args) <= 1 {
			fmt.Println(configSynopsis)
			return nil
		}
		fmt.Printf("unknown config verb %q\n\n%s\n", args[1], configSynopsis)
		askExit(setExit, 2)
		return nil
	}
	c.Subs = map[string]cmd.Command{
		"edit":     editCommand(),
		"list":     listCommand(setExit),
		"schema":   schemaCommand(setExit),
		"validate": validateCommand(deps),
		"publish":  publishCommand(deps),
		"trust":    profileOnlyCommand("trust", "Trust a remote profile target", configTrustHelp, deps, func(ctx context.Context, a []string) error { return deps.profiles.Trust(ctx, a[0]) }),
		"install":  profileOnlyCommand("install", "Install a remote profile target as a local profile", configInstallHelp, deps, func(ctx context.Context, a []string) error { return deps.profiles.Install(ctx, a[0], at(a, 1)) }),
		"update":   profileOnlyCommand("update", "Update installed profiles to their newest version", configUpdateHelp, deps, func(ctx context.Context, a []string) error { return deps.profiles.Update(ctx, firstOrEmpty(a)) }),
		"dir":      dirCommand(),
	}
	return c
}

// editCommand is today's setup wizard under its documented spelling. It takes
// no kind: every positional is one macro stage of the wizard.
func editCommand() *internal.Command {
	c := setup.Command()
	c.Name = "edit"
	c.Desc = "The interactive store editor (today's setup)"
	c.HelpText = `config edit. Starts the interactive store editor (today's setup wizard)
over the whole config store. Positionals are the wizard's macro stages.

Examples:
  clai config edit
  clai -n config edit 1 q   # macro-drive: open model files, then quit`
	return c
}

func listCommand(setExit func(int)) *internal.Command {
	c := &internal.Command{
		Name: "list",
		Desc: "List the store's files",
		HelpText: `config list [kind]. Prints one line per file of the store, or of one kind.
A profile line carries its model, tools, prompt and source stamp; a file that
cannot be read is named and the listing continues.

Examples:
  clai config list
  clai config list profile
  clai config list theme`,
	}
	c.OnRun = func(_ context.Context, c *internal.Command) error {
		args := c.Args()[1:]
		if len(args) > 1 {
			fmt.Println("usage: clai config list [kind]")
			askExit(setExit, 2)
			return nil
		}
		return listStore(firstOrEmpty(args), setExit)
	}
	return c
}

func schemaCommand(setExit func(int)) *internal.Command {
	c := &internal.Command{
		Name: "schema",
		Desc: "Print the config kinds or one kind's document",
		HelpText: `config schema [kind]. With no kind, prints one kind and its version per line,
sorted. With a kind, prints that kind's JSON Schema document byte for byte,
then a newline.

Examples:
  clai config schema
  clai config schema theme
  clai config schema profile > profile.json`,
	}
	c.OnRun = func(_ context.Context, c *internal.Command) error {
		args := c.Args()[1:]
		if len(args) > 1 {
			fmt.Println("usage: clai config schema [kind]")
			askExit(setExit, 2)
			return nil
		}
		if len(args) == 0 {
			printKindVersions()
			return nil
		}
		doc, ok := document(args[0])
		if !ok {
			fmt.Println(unknownKindMessage(args[0]))
			askExit(setExit, 2)
			return nil
		}
		if _, err := os.Stdout.Write([]byte(string(doc) + "\n")); err != nil {
			return diag.Errorf("print the %s schema: %w", args[0], err)
		}
		return nil
	}
	return c
}

func dirCommand() *internal.Command {
	c := &internal.Command{
		Name: "dir",
		Desc: "Print the config dir or a registered subpath",
		HelpText: `config dir [subpath ...]. Prints the config dir, or a registered subpath
within it. Touches no config and creates nothing.

Examples:
  clai config dir
  clai config dir mcpServers`,
	}
	c.OnRun = func(_ context.Context, c *internal.Command) error {
		return confdir.Print(c.Args())
	}
	return c
}

func askExit(setExit func(int), code int) {
	if setExit != nil {
		setExit(code)
	}
}

func firstOrEmpty(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

// allKinds is every kind a verb accepts: the embedded corpus plus profile,
// sorted by name.
func allKinds() []string {
	kinds := make([]string, 0, len(configschema.Kinds())+1)
	for _, kind := range configschema.Kinds() {
		kinds = append(kinds, string(kind))
	}
	kinds = append(kinds, profileKind)
	slices.Sort(kinds)
	return kinds
}

func knownKind(kind string) bool {
	if kind == profileKind {
		return true
	}
	return slices.ContainsFunc(configschema.Kinds(), func(k configschema.Kind) bool {
		return string(k) == kind
	})
}

func unknownKindMessage(kind string) string {
	return fmt.Sprintf("unknown kind %q: expected one of %s", kind, strings.Join(allKinds(), ", "))
}

// document returns kind's schema document byte for byte.
func document(kind string) ([]byte, bool) {
	if kind == profileKind {
		return profile.Schema(), true
	}
	return configschema.Document(configschema.Kind(kind))
}

func printKindVersions() {
	type kindVersion struct{ kind, version string }
	versions := make([]kindVersion, 0, len(configschema.Kinds())+1)
	for _, kind := range configschema.Kinds() {
		versions = append(versions, kindVersion{kind: string(kind), version: configschema.Version(kind)})
	}
	versions = append(versions, kindVersion{kind: profileKind, version: profile.SchemaVersion()})
	slices.SortFunc(versions, func(a, b kindVersion) int { return strings.Compare(a.kind, b.kind) })
	for _, v := range versions {
		fmt.Printf("%s %s\n", v.kind, v.version)
	}
}

// listStore prints one line per file; kind "" lists every kind.
func listStore(kind string, setExit func(int)) error {
	confDir, err := utils.GetClaiConfigDir()
	if err != nil {
		return diag.Errorf("get clai config dir: %w", err)
	}
	if kind == "" {
		for _, k := range allKinds() {
			if err := listKind(confDir, k); err != nil {
				return err
			}
		}
		return nil
	}
	if !knownKind(kind) {
		fmt.Println(unknownKindMessage(kind))
		askExit(setExit, 2)
		return nil
	}
	return listKind(confDir, kind)
}

func listKind(confDir, kind string) error {
	if kind == profileKind {
		return listProfiles(confDir)
	}
	paths, err := kindPaths(confDir, kind)
	if err != nil {
		return err
	}
	for _, path := range paths {
		if _, err := os.ReadFile(path); err != nil {
			fmt.Printf("%s %s: cannot read: %v\n", kind, path, err)
			continue
		}
		fmt.Printf("%s %s\n", kind, path)
	}
	return nil
}

// kindPaths is the store layout of one configschema kind.
func kindPaths(confDir, kind string) ([]string, error) {
	switch configschema.Kind(kind) {
	case configschema.McpServers:
		return jsonFiles(filepath.Join(confDir, "mcpServers"))
	case configschema.ShellContexts:
		return jsonFiles(filepath.Join(confDir, "shellContexts"))
	case configschema.TextConfig:
		return existingFile(filepath.Join(confDir, "textConfig.json"))
	case configschema.PhotoConfig:
		return existingFile(filepath.Join(confDir, "photoConfig.json"))
	case configschema.VideoConfig:
		return existingFile(filepath.Join(confDir, "videoConfig.json"))
	case configschema.Theme:
		return existingFile(filepath.Join(confDir, "theme.json"))
	case configschema.SkillsJSON:
		return existingFile(filepath.Join(confDir, "skills.json"))
	}
	return nil, nil
}

func existingFile(path string) ([]string, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, diag.Errorf("stat %s: %w", path, err)
	}
	return []string{path}, nil
}

func jsonFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, diag.Errorf("read %s: %w", dir, err)
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		paths = append(paths, filepath.Join(dir, entry.Name()))
	}
	return paths, nil
}

// listedProfile is the subset of a profile a listing line prints.
type listedProfile struct {
	Name   string   `json:"name"`
	Model  string   `json:"model"`
	Tools  []string `json:"tools"`
	Prompt string   `json:"prompt"`
	Source *struct {
		Target string `json:"target"`
	} `json:"source"`
}

// listProfiles prints one line per profile file, naming every file it cannot
// read and every file the checker refuses instead of skipping it silently.
func listProfiles(confDir string) error {
	dir := filepath.Join(confDir, "profiles")
	paths, err := jsonFiles(dir)
	if err != nil {
		return err
	}
	if _, err := os.ReadDir(dir); err != nil && os.IsNotExist(err) {
		ancli.Warnf("no profiles directory found at %s\n", dir)
		return nil
	}
	if len(paths) == 0 {
		ancli.Warnf("no profiles found in %s\n", dir)
		return nil
	}
	env, _ := newProfileEnv(confDir)
	for _, path := range paths {
		printProfileLine(path, env)
	}
	return nil
}

func printProfileLine(path string, env profile.Env) {
	body, err := os.ReadFile(path)
	if err != nil {
		printUnreadableProfile(path, err.Error())
		return
	}
	findings, err := profile.Validate(body, profile.File, env)
	if err != nil {
		printUnreadableProfile(path, err.Error())
		return
	}
	if findings.Failed(profile.File) {
		printUnreadableProfile(path, strings.Join(findings.Problems(profile.File), "; "))
		return
	}
	var p listedProfile
	if err := json.Unmarshal(body, &p); err != nil {
		printUnreadableProfile(path, err.Error())
		return
	}
	name := strings.TrimSpace(p.Name)
	if name == "" {
		base := filepath.Base(path)
		name = strings.TrimSuffix(base, filepath.Ext(base))
	}
	fmt.Printf("profile %s: name=%s model=%s tools=%v prompt=%q", path, name, p.Model, p.Tools, firstSentence(p.Prompt))
	if p.Source != nil && p.Source.Target != "" {
		fmt.Printf(" source=%s", p.Source.Target)
	}
	fmt.Println()
}

func printUnreadableProfile(path, reason string) {
	fmt.Printf("profile %s: cannot read: %s. Explain it with: clai profiles validate %s\n", path, reason, path)
}

// firstSentence is the one-line prompt summary a listing shows.
func firstSentence(prompt string) string {
	prompt = utils.RehydrateEscapedConfigString(prompt)
	if prompt == "" {
		return "[Empty prompt]"
	}
	if end := strings.IndexAny(prompt, ".!?\n"); end >= 0 {
		prompt = prompt[:end+1]
	}
	return strings.TrimSpace(strings.ReplaceAll(prompt, "\n", " "))
}

// hiddenAlias is one pre-existing spelling that stays callable and appears in
// no listing. It keeps its wrapped command's flagset, setup and subcommands so
// every flag still parses, rebuilds the documented argument vector on Run and
// re-enters Run in-process, so both spellings share one code path.
type hiddenAlias struct {
	*internal.Command
	canonical func(args []string) ([]string, bool)
	rerun     func(args []string) int
	setExit   func(int)
	// forwardFlags carries the flags the hidden spelling was given into the
	// canonical run, which parses them on a command of its own.
	forwardFlags bool
}

// newHiddenAlias wraps wrapped. The clone drops OnSetup: the canonical run
// repeats it once, so delegating would run the config prep twice.
func newHiddenAlias(wrapped *internal.Command, canonical func([]string) ([]string, bool), rerun func([]string) int, setExit func(int)) *hiddenAlias {
	clone := *wrapped
	clone.OnSetup = nil
	return &hiddenAlias{Command: &clone, canonical: canonical, rerun: rerun, setExit: setExit}
}

// Hidden keeps the alias out of help, completion and every synopsis.
func (h *hiddenAlias) Hidden() bool { return true }

func (h *hiddenAlias) Run(ctx context.Context) error {
	if h.canonical != nil {
		if args, ok := h.canonical(h.Args()); ok {
			if h.forwardFlags {
				args = append(args, setFlagTokens(h.Flagset())...)
			}
			if code := h.rerun(args); code != 0 {
				askExit(h.setExit, code)
			}
			return nil
		}
	}
	return h.Command.Run(ctx)
}

// newVerbAlias wraps one subcommand of a hidden spelling so it re-enters Run
// as the config verb that now documents it, flags included: `profiles
// validate x -json` becomes `config validate profile x -json`.
func newVerbAlias(wrapped *internal.Command, canonical func(args []string) ([]string, bool), rerun func([]string) int, setExit func(int)) *hiddenAlias {
	alias := newHiddenAlias(wrapped, canonical, rerun, setExit)
	alias.forwardFlags = true
	return alias
}

// setFlagTokens renders every flag the hidden spelling was given as `-name
// [value]` tokens, so the config verb can parse the same options.
func setFlagTokens(fs *flag.FlagSet) []string {
	var out []string
	fs.Visit(func(f *flag.Flag) {
		out = append(out, "-"+f.Name)
		if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
			return
		}
		out = append(out, f.Value.String())
	})
	return out
}

// hiddenCommand keeps a command's own dispatch and only takes it out of every
// listing: the pre-existing spelling of a verb whose behaviour the config tree
// did not take over, such as `cloud publish`'s wizard.
type hiddenCommand struct{ *internal.Command }

// Hidden keeps the spelling out of help, completion and every synopsis.
func (h *hiddenCommand) Hidden() bool { return true }

// newHiddenCommand wraps wrapped so it stays callable and appears in no
// listing.
func newHiddenCommand(wrapped *internal.Command) *hiddenCommand {
	return &hiddenCommand{Command: wrapped}
}

// setupAliasArgs rebuilds `setup|s ...` as `config edit [-n] ...`: the
// non-interactive flag reaches the canonical verb, and every positional stays
// a wizard macro stage.
func setupAliasArgs(nonInteractive *internal.NonInteractiveFlag) func([]string) ([]string, bool) {
	return func(args []string) ([]string, bool) {
		canonical := []string{"config", "edit"}
		if nonInteractive != nil && nonInteractive.Value() {
			canonical = append(canonical, "-n")
		}
		return append(canonical, args[1:]...), true
	}
}

// listProfileAliasArgs rebuilds bare `profiles` as `config list profile`; the
// profiles subcommands this part does not own keep their own dispatch.
func listProfileAliasArgs(args []string) ([]string, bool) {
	if len(args) <= 1 {
		return []string{"config", "list", "profile"}, true
	}
	return nil, false
}

// dirAliasArgs rebuilds `confdir [subpath]` as `config dir [subpath]`.
func dirAliasArgs(args []string) ([]string, bool) {
	return append([]string{"config", "dir"}, args[1:]...), true
}

// verbAliasArgs rebuilds `<old spelling> <args...>` as `config <verb>
// <args...>`: every pre-existing subcommand of one hidden spelling keeps its
// positionals and gains the config verb's kind.
func verbAliasArgs(verb ...string) func([]string) ([]string, bool) {
	return func(args []string) ([]string, bool) {
		canonical := append([]string{"config"}, verb...)
		return append(canonical, args[1:]...), true
	}
}

// ----- validate -----

const configValidateHelp = `config validate <kind> <target> [-json]. Checks one target against its kind.
Kind profile runs the full profile check over the profile target grammar: a
name, a path, cchub:, gh: or https:. Every other kind reads a file and applies
that kind's JSON Schema document. It never runs the target and writes no file.

Findings print one per line, severity member: reason [routes], on stdout; -json
prints one array and nothing else. Exit 0 when no Error applies to any route, 1
when one does, 2 on an unknown kind, a missing target or an unreadable file.

Examples:
  clai config validate profile ./reviewer.json
  clai config validate profile cchub:lorentz/trade-bot@3
  clai config validate theme ./theme.json
  clai config validate profile reviewer -json`

func validateCommand(deps configDeps) *internal.Command {
	var asJSON bool
	c := &internal.Command{
		Name:     "validate",
		Desc:     "Print what a run or publish would refuse for one target",
		HelpText: configValidateHelp,
		Register: func(fs *flag.FlagSet) {
			fs.BoolVar(&asJSON, "json", false, "print the findings as one JSON array and nothing else")
		},
	}
	c.OnRun = func(ctx context.Context, c *internal.Command) error {
		pos, err := verbPositionals(c.Flagset(), c.Args()[1:])
		if err != nil {
			printValidateUsage(deps.setExit, err.Error())
			return nil
		}
		switch {
		case len(pos) < 2:
			printValidateUsage(deps.setExit, "")
			return nil
		case len(pos) > 2:
			printValidateUsage(deps.setExit, fmt.Sprintf("unexpected argument %q: validate takes a kind and one target", pos[2]))
			return nil
		}
		kind, target := pos[0], pos[1]
		if !knownKind(kind) {
			fmt.Println(unknownKindMessage(kind))
			askExit(deps.setExit, 2)
			return nil
		}
		if kind == profileKind {
			status, err := deps.profiles.Validate(ctx, target, asJSON)
			if err != nil {
				return err
			}
			askExit(deps.setExit, int(status))
			return nil
		}
		return validateSchemaTarget(kind, target, asJSON, deps.setExit)
	}
	return c
}

// printValidateUsage prints one usage line, naming the offending argument when
// there is one, and asks for exit 2.
func printValidateUsage(setExit func(int), problem string) {
	line := "usage: clai config validate <kind> <target> [-json]"
	if problem != "" {
		line += ": " + problem
	}
	fmt.Println(line)
	askExit(setExit, 2)
}

// validateSchemaTarget applies target's kind document and prints its findings.
func validateSchemaTarget(kind, target string, asJSON bool, setExit func(int)) error {
	body, err := os.ReadFile(target)
	if err != nil {
		return internal.StatusError(2, diag.Errorf("read the %s target %s: %w", kind, target, err))
	}
	findings, err := configschema.Check(configschema.Kind(kind), body)
	if err != nil {
		return internal.StatusError(2, err)
	}
	if err := printConfigFindings(findings, asJSON); err != nil {
		return err
	}
	if slices.ContainsFunc(findings, func(f configschema.Finding) bool { return f.Severity == configschema.Error }) {
		askExit(setExit, 1)
	}
	return nil
}

// configFindingJSON is a schema finding as -json prints it.
type configFindingJSON struct {
	Severity string   `json:"severity"`
	Member   string   `json:"member"`
	Reason   string   `json:"reason"`
	Routes   []string `json:"routes"`
}

// printConfigFindings writes one line per finding, or the findings as one
// JSON array and nothing else.
func printConfigFindings(findings []configschema.Finding, asJSON bool) error {
	if asJSON {
		out := make([]configFindingJSON, 0, len(findings))
		for _, f := range findings {
			out = append(out, configFindingJSON{
				Severity: configSeverityName(f.Severity),
				Member:   f.Member,
				Reason:   f.Reason,
				Routes:   []string{"file", "remote", "publish"},
			})
		}
		encoded, err := json.Marshal(out)
		if err != nil {
			return diag.Errorf("encode findings: %w", err)
		}
		fmt.Printf("%s\n", encoded)
		return nil
	}
	for _, f := range findings {
		fmt.Printf("%s %s: %s [*]\n", configSeverityName(f.Severity), f.Member, f.Reason)
	}
	return nil
}

func configSeverityName(severity configschema.Severity) string {
	if severity == configschema.Warning {
		return "warning"
	}
	return "error"
}

// verbPositionals collects a verb's positionals, parsing flags wherever they
// are written, since the dispatcher stops at the first positional.
func verbPositionals(fs *flag.FlagSet, args []string) ([]string, error) {
	fs.SetOutput(io.Discard)
	var out []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			break
		}
		out = append(out, args[0])
		args = args[1:]
	}
	return out, nil
}

// ----- publish -----

const configPublishHelp = `config publish [kind] [name] [mode] [flags]. Publishes a model alias, profile
or skill to the Hub as your clai.chat account (clai cloud login first). A first
positional that names a hub kind is the kind; otherwise it is the name, and
the kind is inferred from the items that carry it, or asked when several do.
A missing name or mode asks on stdin, and piped input answers the prompts in
order; -n makes a missing answer an error.

Examples:
  clai config publish
  clai config publish profile trade-bot -note 'tighter stop-loss'
  clai config publish alias strong photo
  clai config publish skill review`

func publishCommand(deps configDeps) *internal.Command {
	var opts cloudcmd.PublishOptions
	nonInteractive := &internal.NonInteractiveFlag{}
	c := &internal.Command{
		Name:           "publish",
		Desc:           "Publish an alias, profile or skill to the Hub",
		HelpText:       configPublishHelp,
		NonInteractive: nonInteractive,
		Register: func(fs *flag.FlagSet) {
			fs.StringVar(&opts.Name, "name", "", "the name in its address (default: the alias, profile or skill name)")
			fs.StringVar(&opts.Title, "title", "", "the title on the Hub (default: the name; kept on a new version)")
			fs.StringVar(&opts.Summary, "summary", "", "one line on what it does (kept on a new version)")
			fs.StringVar(&opts.Note, "note", "", "what changed in this version")
			fs.StringVar(&opts.Handle, "handle", "", "your handle, needed once on an account without one")
			nonInteractive.Register(fs)
		},
	}
	c.OnRun = func(ctx context.Context, c *internal.Command) error {
		pos, err := verbPositionals(c.Flagset(), c.Args()[1:])
		if err != nil {
			return err
		}
		return deps.publishResolved(ctx, pos, opts, nonInteractive.Value())
	}
	return c
}

// publishResolved fills in the levels the arguments leave out and walks the
// publish code path every spelling shares.
func (d configDeps) publishResolved(ctx context.Context, args []string, opts cloudcmd.PublishOptions, nonInteractive bool) error {
	var kind, name, mode string
	rest := args
	if len(rest) > 0 && isHubKind(rest[0]) {
		kind, rest = rest[0], rest[1:]
	}
	if len(rest) > 0 {
		name, rest = rest[0], rest[1:]
	}
	if len(rest) > 0 {
		mode, rest = rest[0], rest[1:]
	}
	if len(rest) > 0 {
		return fmt.Errorf("unexpected argument %q: clai config publish [kind] [name] [mode]", rest[0])
	}

	var in io.Reader
	var reader *bufio.Reader
	if !nonInteractive {
		reader = bufio.NewReader(os.Stdin)
		in = reader
	}
	var err error
	if kind == "" {
		if name == "" {
			if kind, err = askHubKind(reader); err != nil {
				return err
			}
		} else {
			if kind, err = d.inferKind(name, reader); err != nil {
				return err
			}
		}
	}
	if name == "" {
		if reader == nil {
			return &profiles.MissingPublishArgError{Usage: "clai config publish " + kind + " <name>"}
		}
		if name, err = askLine(reader, "name"); err != nil {
			return err
		}
	}
	publishArgs := []string{kind, name}
	if mode != "" {
		publishArgs = append(publishArgs, mode)
	}
	opts.In = in
	return d.publish(ctx, publishArgs, opts)
}

// inferKind names the kind a bare publish name belongs to: the one kind whose
// items carry it, the kind asked for when several do, and profile for a path
// no discovery sees.
func (d configDeps) inferKind(name string, reader *bufio.Reader) (string, error) {
	kinds, err := d.profiles.KindsCarrying(name)
	if err != nil {
		return "", err
	}
	switch len(kinds) {
	case 1:
		return kinds[0], nil
	case 0:
		if looksLikePath(name) {
			return "profile", nil
		}
		return "", fmt.Errorf("no alias, profile or skill named %q: pass a kind, as in clai config publish <%s> %s",
			name, strings.Join(profiles.HubKindNames(), "|"), name)
	default:
		return askAmong(reader, kinds, fmt.Sprintf("which kind for %q", name))
	}
}

// askHubKind asks which hub kind to publish, when nobody named one.
func askHubKind(reader *bufio.Reader) (string, error) {
	if reader == nil {
		return "", &profiles.MissingPublishArgError{Usage: "clai config publish <" + strings.Join(profiles.HubKindNames(), "|") + ">"}
	}
	answer, err := askLine(reader, "publish kind ("+strings.Join(profiles.HubKindNames(), "|")+")")
	if err != nil {
		return "", err
	}
	if !isHubKind(answer) {
		return "", fmt.Errorf("unknown publish kind %q: expected %s", answer, strings.Join(profiles.HubKindNames(), ", "))
	}
	return answer, nil
}

// askAmong asks which of options is meant.
func askAmong(reader *bufio.Reader, options []string, prompt string) (string, error) {
	if reader == nil {
		return "", &profiles.MissingPublishArgError{Usage: "pass it, one of " + strings.Join(options, ", ")}
	}
	answer, err := askLine(reader, prompt+" ("+strings.Join(options, "|")+")")
	if err != nil {
		return "", err
	}
	if !slices.Contains(options, answer) {
		return "", fmt.Errorf("unknown answer %q: expected %s", answer, strings.Join(options, ", "))
	}
	return answer, nil
}

// askLine prints a prompt and reads one trimmed line.
func askLine(reader *bufio.Reader, prompt string) (string, error) {
	fmt.Printf("%s: ", prompt)
	line, err := reader.ReadString('\n')
	answer := strings.TrimSpace(line)
	if err != nil && answer == "" {
		return "", fmt.Errorf("no answer on stdin for %q: pass it, or run where a person can answer", prompt)
	}
	return answer, nil
}

// isHubKind reports whether name is a kind publish accepts.
func isHubKind(name string) bool {
	return slices.Contains(profiles.HubKindNames(), name)
}

// looksLikePath reports a name no discovery sees but a profile path can name.
func looksLikePath(name string) bool {
	return strings.ContainsRune(name, os.PathSeparator) || strings.HasSuffix(name, ".json")
}

// ----- trust, install, update -----

const configTrustHelp = `config trust profile <target>. Fetches a remote profile target, shows what it
runs, and records its hash in <config-dir>/trust.json (never synced), so a run
with -prp runs it without asking, in cron and scripts too.

Examples:
  clai config trust profile cchub:lorentz/trade-bot@4`

const configInstallHelp = `config install profile <target> [name]. Fetches a remote profile target and
saves it as <config-dir>/profiles/<name>.json with a source stamp, so
clai -profile <name> runs it offline. The name defaults to the agent's, repo's
or file's name.

Examples:
  clai config install profile cchub:lorentz/trade-bot@3
  clai config install profile gh:baalimago/agents/review.json@v1 reviewer`

const configUpdateHelp = `config update profile [name]. Fetches the newest version of every installed
profile, or of the one named, and rewrites those that changed.

Examples:
  clai config update profile
  clai config update profile trade-bot`

// profileOnlyCommand builds a trust, install or update verb that exists only
// for kind profile; any other kind exits 2 and names the operation that does
// not exist for it.
func profileOnlyCommand(name, desc, help string, deps configDeps, run func(context.Context, []string) error) *internal.Command {
	minArgs, maxArgs := 1, 1
	usage := "clai config " + name + " profile"
	switch name {
	case "install":
		maxArgs, usage = 2, "clai config install profile <target> [name]"
	case "update":
		minArgs, usage = 0, "clai config update profile [name]"
	case "trust":
		usage = "clai config trust profile <target>"
	}
	c := &internal.Command{Name: name, Desc: desc, HelpText: help}
	c.OnRun = func(ctx context.Context, c *internal.Command) error {
		pos := c.Args()[1:]
		if len(pos) == 0 {
			fmt.Println("usage: " + usage)
			askExit(deps.setExit, 2)
			return nil
		}
		if pos[0] != profileKind {
			fmt.Printf("config %s does not exist for kind %q: the operation is %s\n", name, pos[0], usage)
			askExit(deps.setExit, 2)
			return nil
		}
		args := pos[1:]
		if len(args) < minArgs || len(args) > maxArgs {
			fmt.Println("usage: " + usage)
			askExit(deps.setExit, 2)
			return nil
		}
		return run(ctx, args)
	}
	return c
}

func at(args []string, i int) string {
	if i < len(args) {
		return args[i]
	}
	return ""
}
