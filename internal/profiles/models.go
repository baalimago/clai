package profiles

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/baalimago/clai/internal/modelalias"
	"github.com/baalimago/clai/internal/remote"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/table"
)

// aliasMode is a mode config that can hold model aliases.
type aliasMode struct {
	name, file string
	use        func(pinned string) string
}

// aliasModes is sorted by name, the order selectors and mode indices use.
var aliasModes = []aliasMode{
	{name: "audio", file: "audioConfig.json", use: func(p string) string { return "clai audio transcribe -audio-model " + p + " …" }},
	{name: "photo", file: "photoConfig.json", use: func(p string) string { return "clai -photo-model " + p + " photo …" }},
	{name: "text", file: "textConfig.json", use: func(p string) string { return "clai -chat-model " + p + " query …" }},
}

// UnknownAliasModeError is a mode that names no mode config.
type UnknownAliasModeError struct{ Mode string }

func (e *UnknownAliasModeError) Error() string {
	return fmt.Sprintf("unknown mode %q: expected text, photo or audio", e.Mode)
}

// AmbiguousAliasError is an alias defined in more than one mode config with
// no mode given and nobody to ask.
type AmbiguousAliasError struct {
	Alias string
	Modes []string
}

func (e *AmbiguousAliasError) Error() string {
	return fmt.Sprintf("model alias %q is defined in modes %s: pick one with clai cloud publish alias %s <%s>",
		e.Alias, strings.Join(e.Modes, ", "), e.Alias, strings.Join(e.Modes, "|"))
}

func aliasModeByName(name string) (aliasMode, error) {
	for _, m := range aliasModes {
		if m.name == name {
			return m, nil
		}
	}
	return aliasMode{}, &UnknownAliasModeError{Mode: name}
}

// definedAlias is one mode config's definition of an alias.
type definedAlias struct {
	mode   aliasMode
	path   string
	models []string
}

func (a definedAlias) label() string {
	return fmt.Sprintf("%-6s %s", a.mode.name, strings.Join(a.models, ", "))
}

// PublishAlias sends a local model alias (by name from mode configs or by a
// .json file path) to the Hub as a deduplicated JSON array of model names.
// An alias defined in several mode configs needs opts.Mode, or opts.In to
// ask which one.
func (d CommandDeps) PublishAlias(ctx context.Context, alias string, opts PublishOptions) error {
	confDir, err := d.configDir()
	if err != nil {
		return err
	}
	source, raw, use, err := d.loadAlias(confDir, alias, opts)
	if err != nil {
		return err
	}
	name := opts.Name
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(alias), ".json")
	}
	if !hubName.MatchString(name) {
		return fmt.Errorf("%q cannot be a Hub name (lower-case letters, digits and dashes); pass one with -name", name)
	}
	models, err := remote.ValidateModelList(source, raw)
	if err != nil {
		return err
	}
	body, err := json.Marshal(models)
	if err != nil {
		return fmt.Errorf("marshal model alias %s: %w", name, err)
	}
	if d.FindSecret != nil {
		if where, found := d.FindSecret(source, body); found {
			return fmt.Errorf("%s holds what looks like a credential (%s): remove it, then publish", source, where)
		}
	}
	return d.send(ctx, confDir, alias, PublishRequest{
		Kind: "alias", Name: name, Body: string(body), Title: opts.Title, Summary: opts.Summary, Note: opts.Note, Handle: opts.Handle,
	}, use)
}

func isAliasFile(alias string) bool {
	return strings.ContainsRune(alias, os.PathSeparator) || strings.HasSuffix(alias, ".json")
}

func (d CommandDeps) loadAlias(confDir, alias string, opts PublishOptions) (source string, raw []byte, use func(string) string, err error) {
	if isAliasFile(alias) {
		mode, err := aliasModeByName(defaultString(opts.Mode, "text"))
		if err != nil {
			return "", nil, nil, err
		}
		b, err := os.ReadFile(alias)
		if err != nil {
			return "", nil, nil, fmt.Errorf("read model alias %s: %w", alias, err)
		}
		return alias, b, mode.use, nil
	}
	defined, err := aliasDefinitions(confDir, alias)
	if err != nil {
		return "", nil, nil, err
	}
	chosen, err := d.chooseMode(alias, defined, opts)
	if err != nil {
		return "", nil, nil, err
	}
	b, err := json.Marshal(chosen.models)
	if err != nil {
		return "", nil, nil, fmt.Errorf("marshal alias %s: %w", alias, err)
	}
	return chosen.path, b, chosen.mode.use, nil
}

func defaultString(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// aliasDefinitions returns every mode config's resolved definition of
// alias, in mode-name order; none is fs.ErrNotExist.
func aliasDefinitions(confDir, alias string) ([]definedAlias, error) {
	var defined []definedAlias
	for _, m := range aliasModes {
		path := filepath.Join(confDir, m.file)
		aliases, err := readConfigAliases(path)
		if err != nil {
			return nil, err
		}
		if _, exists := aliases[alias]; !exists {
			continue
		}
		models, err := aliases.Resolve(alias)
		if err != nil {
			return nil, err
		}
		defined = append(defined, definedAlias{mode: m, path: path, models: models})
	}
	if len(defined) == 0 {
		return nil, fmt.Errorf("model alias %q: %w in %s", alias, fs.ErrNotExist, confDir)
	}
	return defined, nil
}

func (d CommandDeps) chooseMode(alias string, defined []definedAlias, opts PublishOptions) (definedAlias, error) {
	if opts.Mode != "" {
		return pickMode(alias, defined, opts.Mode)
	}
	if len(defined) == 1 {
		return defined[0], nil
	}
	if opts.In == nil {
		modes := make([]string, 0, len(defined))
		for _, a := range defined {
			modes = append(modes, a.mode.name)
		}
		return definedAlias{}, &AmbiguousAliasError{Alias: alias, Modes: modes}
	}
	i, err := selectOne(d, opts.In, "Publish alias "+alias+" from mode", defined, definedAlias.label)
	if err != nil {
		return definedAlias{}, err
	}
	return defined[i], nil
}

// pickMode takes a mode by name, or by index among the modes defining alias.
func pickMode(alias string, defined []definedAlias, mode string) (definedAlias, error) {
	if i, err := strconv.Atoi(mode); err == nil {
		if i < 0 || i >= len(defined) {
			return definedAlias{}, fmt.Errorf("mode index %d out of range: model alias %q is defined in %d modes", i, alias, len(defined))
		}
		return defined[i], nil
	}
	m, err := aliasModeByName(mode)
	if err != nil {
		return definedAlias{}, err
	}
	for _, a := range defined {
		if a.mode.name == m.name {
			return a, nil
		}
	}
	return definedAlias{}, fmt.Errorf("model alias %q: %w in %s", alias, fs.ErrNotExist, m.file)
}

func readConfigAliases(path string) (modelalias.Table, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg struct {
		ModelAliases modelalias.Table `json:"model-aliases"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg.ModelAliases, nil
}

// MissingPublishArgError is a publish level left out with nobody to ask.
type MissingPublishArgError struct{ Usage string }

func (e *MissingPublishArgError) Error() string {
	return "no terminal to ask on: pass it, as in " + e.Usage
}

// publishKind is one branch of the publish tree.
type publishKind struct {
	name     string
	discover func(confDir string) ([]publishable, error)
	publish  func(d CommandDeps, ctx context.Context, name string, opts PublishOptions) error
}

// publishable is one item a kind lists.
type publishable struct {
	name, detail string
}

func (p publishable) label() string {
	if p.detail == "" {
		return p.name
	}
	return fmt.Sprintf("%-18s  %s", p.name, p.detail)
}

var publishKinds = []publishKind{
	{name: "alias", discover: discoverAliases, publish: CommandDeps.PublishAlias},
	{name: "profile", discover: discoverProfiles, publish: CommandDeps.Publish},
	{name: "skill", discover: discoverSkills, publish: CommandDeps.PublishSkill},
}

// PublishInteractive walks `clai cloud publish [kind [name [mode]]]`: each
// argument given picks its level, and each one missing is asked on in. A nil
// in means nobody can answer, so a missing level is an error.
func (d CommandDeps) PublishInteractive(ctx context.Context, in io.Reader, args []string, opts PublishOptions) error {
	confDir, err := d.configDir()
	if err != nil {
		return err
	}
	kind, err := d.pickKind(in, args)
	if err != nil {
		return err
	}
	maxArgs := 2
	if kind.name == "alias" {
		maxArgs = 3
	}
	if len(args) > maxArgs {
		return fmt.Errorf("unexpected argument %q: clai cloud publish %s takes at most %d arguments", args[maxArgs], kind.name, maxArgs-1)
	}
	if len(args) == 3 {
		opts.Mode = args[2]
	}
	opts.In = in
	if len(args) >= 2 {
		return kind.publish(d, ctx, args[1], opts)
	}
	items, err := kind.discover(confDir)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return fmt.Errorf("no %s to publish in %s", kind.name, confDir)
	}
	if in == nil {
		return &MissingPublishArgError{Usage: "clai cloud publish " + kind.name + " <name>"}
	}
	i, err := selectOne(d, in, "Publish "+kind.name, items, publishable.label)
	if err != nil {
		return err
	}
	return kind.publish(d, ctx, items[i].name, opts)
}

func (d CommandDeps) pickKind(in io.Reader, args []string) (publishKind, error) {
	if len(args) > 0 {
		for _, k := range publishKinds {
			if k.name == args[0] {
				return k, nil
			}
		}
		return publishKind{}, fmt.Errorf("unknown publish kind %q: expected alias, profile or skill", args[0])
	}
	if in == nil {
		return publishKind{}, &MissingPublishArgError{Usage: "clai cloud publish <alias|profile|skill>"}
	}
	i, err := selectOne(d, in, "Publish to Hub", publishKinds, func(k publishKind) string { return k.name })
	if err != nil {
		return publishKind{}, err
	}
	return publishKinds[i], nil
}

// selectOne asks on in for one of items and returns its index.
func selectOne[T any](d CommandDeps, in io.Reader, header string, items []T, label func(T) string) (int, error) {
	out := d.Out
	if out == nil {
		out = os.Stdout
	}
	selected, _, err := table.New(
		table.SlicePaginator(items),
		func(i int, item T) (string, error) {
			return fmt.Sprintf("%d. %s", i, label(item)), nil
		},
	).
		WithHeader(header).
		WithPageSize(utils.TableTheme().Items).
		WithSingleSelect().
		WithWriter(out).
		WithInput(in).
		Run()
	if err != nil {
		return 0, fmt.Errorf("%s: %w", strings.ToLower(header), err)
	}
	if len(selected) == 0 || selected[0] < 0 || selected[0] >= len(items) {
		return 0, fmt.Errorf("%s: nothing selected", strings.ToLower(header))
	}
	return selected[0], nil
}

func discoverAliases(confDir string) ([]publishable, error) {
	modes := make(map[string][]string)
	for _, m := range aliasModes {
		aliases, err := readConfigAliases(filepath.Join(confDir, m.file))
		if err != nil {
			return nil, err
		}
		for name := range aliases {
			modes[name] = append(modes[name], m.name)
		}
	}
	items := make([]publishable, 0, len(modes))
	for name, ms := range modes {
		items = append(items, publishable{name: name, detail: strings.Join(ms, ", ")})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].name < items[j].name })
	return items, nil
}

func discoverProfiles(confDir string) ([]publishable, error) {
	entries, err := readDirIfExists(filepath.Join(confDir, "profiles"))
	if err != nil {
		return nil, err
	}
	var items []publishable
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
			items = append(items, publishable{name: strings.TrimSuffix(e.Name(), ".json")})
		}
	}
	return items, nil
}

func discoverSkills(confDir string) ([]publishable, error) {
	dir := filepath.Join(confDir, "skills")
	entries, err := readDirIfExists(dir)
	if err != nil {
		return nil, err
	}
	var items []publishable
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, e.Name(), remote.SkillFile)); err == nil {
			items = append(items, publishable{name: e.Name()})
		}
	}
	return items, nil
}

func readDirIfExists(dir string) ([]os.DirEntry, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	return entries, nil
}
