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
	"regexp"
	"sort"
	"strings"

	"github.com/baalimago/clai/internal/remote"
)

// NewResolverFunc builds the remote-target resolver over a config dir.
// The profiles subcommands are the user's consent, so they never prompt.
type NewResolverFunc func(confDir string, prompt remote.Prompter) (remote.Resolver, error)

// CommandDeps are the profiles command's collaborators.
type CommandDeps struct {
	// NewResolver resolves targets for trust, install and update; nil
	// leaves only list.
	NewResolver NewResolverFunc
	// ConfigDir returns the clai config directory.
	ConfigDir func() (string, error)
	Out       io.Writer
}

// Source is the stamp install writes into a profile: the target it came
// from, pinned by hash. update follows it; the hub shows "based on" it.
type Source struct {
	Target string `json:"target"`
}

var profileName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ErrNotInstalled is returned by update for a profile without a source.
var ErrNotInstalled = errors.New("not installed from a remote target")

func (d CommandDeps) resolver(confDir string) (remote.Resolver, error) {
	if d.NewResolver == nil {
		return remote.Resolver{}, fmt.Errorf("remote profile targets are not available in this build")
	}
	return d.NewResolver(confDir, nil)
}

func (d CommandDeps) configDir() (string, error) {
	if d.ConfigDir == nil {
		return "", fmt.Errorf("no config directory")
	}
	return d.ConfigDir()
}

// Trust fetches target, shows what it runs and records it as trusted, so
// a later -prp run, a cron job included, runs it without asking.
func (d CommandDeps) Trust(ctx context.Context, target string) error {
	confDir, err := d.configDir()
	if err != nil {
		return err
	}
	r, err := d.resolver(confDir)
	if err != nil {
		return err
	}
	res, err := r.Resolve(ctx, target, remote.ConsentGiven)
	if err != nil {
		return err
	}
	fmt.Fprintf(d.Out, "%s\nTrusted %s (sha256 %s) in %s\n", res.Summary, res.Target, res.Digest, filepath.Join(confDir, remote.TrustFile))
	return nil
}

// Install fetches target and saves it as <config>/profiles/<name>.json
// with a source stamp, so clai -p <name> runs it offline from then on.
func (d CommandDeps) Install(ctx context.Context, target, name string) error {
	confDir, err := d.configDir()
	if err != nil {
		return err
	}
	r, err := d.resolver(confDir)
	if err != nil {
		return err
	}
	t, err := remote.ParseTarget(target)
	if err != nil {
		return err
	}
	if name == "" {
		name = t.DefaultName()
	}
	if !profileName.MatchString(name) {
		return fmt.Errorf("%q is not a profile name: use letters, digits, '.', '_' and '-'", name)
	}
	path := filepath.Join(confDir, "profiles", name+".json")
	existing, err := readSource(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case errors.Is(err, ErrNotInstalled):
		return fmt.Errorf("profile %q already exists and was not installed from a remote target; pick another name", name)
	case err != nil:
		return err
	default:
		prev, perr := remote.ParseTarget(existing.Target)
		if perr != nil || prev.Base() != t.Base() {
			return fmt.Errorf("profile %q was installed from %s; pick another name", name, existing.Target)
		}
	}
	res, err := r.Resolve(ctx, target, remote.ConsentGiven)
	if err != nil {
		return err
	}
	if err := writeStamped(path, res); err != nil {
		return err
	}
	fmt.Fprintf(d.Out, "%s\nInstalled %s as %s. Run it with: clai -p %s q …\n", res.Summary, res.Target.WithDigest(res.Digest), path, name)
	return nil
}

// Update re-fetches the newest version of each installed profile, or of
// the one named, and rewrites those that changed.
func (d CommandDeps) Update(ctx context.Context, name string) error {
	confDir, err := d.configDir()
	if err != nil {
		return err
	}
	r, err := d.resolver(confDir)
	if err != nil {
		return err
	}
	dir := filepath.Join(confDir, "profiles")
	names, err := installedNames(dir, name)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		fmt.Fprintln(d.Out, "No profile was installed from a remote target.")
		return nil
	}
	var failed []error
	for _, n := range names {
		if err := d.updateOne(ctx, r, filepath.Join(dir, n+".json"), n); err != nil {
			failed = append(failed, fmt.Errorf("%s: %w", n, err))
		}
	}
	return errors.Join(failed...)
}

func (d CommandDeps) updateOne(ctx context.Context, r remote.Resolver, path, name string) error {
	src, err := readSource(path)
	if err != nil {
		return err
	}
	t, err := remote.ParseTarget(src.Target)
	if err != nil {
		return fmt.Errorf("source stamp: %w", err)
	}
	res, err := r.Resolve(ctx, t.Unpinned().String(), remote.ConsentGiven)
	if err != nil {
		return err
	}
	if res.Digest == t.SHA256 {
		fmt.Fprintf(d.Out, "%s is up to date with %s\n", name, res.Target)
		return nil
	}
	if err := writeStamped(path, res); err != nil {
		return err
	}
	fmt.Fprintf(d.Out, "%s\nUpdated %s to %s (sha256 %s)\n", res.Summary, name, res.Target, res.Digest)
	return nil
}

func installedNames(dir, only string) ([]string, error) {
	if only != "" {
		if _, err := readSource(filepath.Join(dir, only+".json")); err != nil {
			return nil, fmt.Errorf("profile %q: %w", only, err)
		}
		return []string{only}, nil
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read profiles: %w", err)
	}
	var names []string
	for _, e := range entries {
		n, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || e.IsDir() {
			continue
		}
		if _, err := readSource(filepath.Join(dir, e.Name())); err == nil {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names, nil
}

func readSource(path string) (Source, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Source{}, err
	}
	var p struct {
		Source *Source `json:"source"`
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return Source{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if p.Source == nil || strings.TrimSpace(p.Source.Target) == "" {
		return Source{}, ErrNotInstalled
	}
	return *p.Source, nil
}

func writeStamped(path string, res remote.Resolved) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(res.Body, &fields); err != nil {
		return fmt.Errorf("parse %s: %w", res.Target, err)
	}
	stamp, err := json.Marshal(Source{Target: res.Target.WithDigest(res.Digest)})
	if err != nil {
		return fmt.Errorf("encode source stamp: %w", err)
	}
	fields["source"] = stamp
	out, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return fmt.Errorf("encode profile: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create profiles dir: %w", err)
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
