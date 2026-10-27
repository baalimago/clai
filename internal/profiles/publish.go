package profiles

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/clai/internal/remote"
)

// PublishRequest is what `clai profiles publish` sends to the Hub.
type PublishRequest struct {
	// Kind is "skill" for a skill; empty publishes a profile.
	Kind    string `json:"kind,omitempty"`
	Name    string `json:"name"`
	Body    string `json:"body"`
	Title   string `json:"title,omitempty"`
	Summary string `json:"summary,omitempty"`
	Note    string `json:"note,omitempty"`
	Handle  string `json:"handle,omitempty"`
}

// Published is the Hub's answer: the agent's address, the version just
// made, and the hash of its bytes.
type Published struct {
	Address string `json:"address"`
	Version int    `json:"version"`
	Created bool   `json:"created"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
}

// HubPublisher sends a profile to the Hub as the logged-in account.
type HubPublisher func(ctx context.Context, req PublishRequest) (Published, error)

// NewHubPublisherFunc builds the publisher over the config dir's login.
type NewHubPublisherFunc func(confDir string) (HubPublisher, error)

// FindSecretFunc reports where a profile holds a credential.
type FindSecretFunc func(name string, b []byte) (where string, found bool)

// HandleRequiredError is the Hub asking an account without a handle to
// pick one.
type HandleRequiredError struct {
	Suggested string
}

func (e *HandleRequiredError) Error() string {
	return "your account has no handle yet; pick one with -handle"
}

// PublishOptions are publish's flags.
type PublishOptions struct {
	Name, Title, Summary, Note, Handle string
}

var hubName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,58}[a-z0-9])?$`)

// Publish sends a local profile, by name or path, to the Hub. It is checked
// as strictly as a remote profile and for credentials before it leaves.
func (d CommandDeps) Publish(ctx context.Context, profile string, opts PublishOptions) error {
	confDir, err := d.configDir()
	if err != nil {
		return err
	}
	path := profile
	if !strings.ContainsRune(profile, os.PathSeparator) && !strings.HasSuffix(profile, ".json") {
		path = filepath.Join(confDir, "profiles", profile+".json")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read profile %s: %w", profile, err)
	}
	name := opts.Name
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(path), ".json")
	}
	if !hubName.MatchString(name) {
		return fmt.Errorf("%q cannot be a Hub name (lower-case letters, digits and dashes); pass one with -name", name)
	}
	if _, err := remote.Validate(path, body); err != nil {
		return err
	}
	if err := pinnedSkills(body); err != nil {
		return err
	}
	if err := noShellContext(body); err != nil {
		return err
	}
	if d.FindSecret != nil {
		if where, found := d.FindSecret(path, body); found {
			return fmt.Errorf("%s holds what looks like a credential (%s): move it to an envfile or an environment variable, then publish", path, where)
		}
	}
	return d.send(ctx, confDir, profile, PublishRequest{
		Name: name, Body: string(body), Title: opts.Title, Summary: opts.Summary, Note: opts.Note, Handle: opts.Handle,
	}, func(pinned string) string { return "clai -profile-path " + pinned + " query …" })
}

// send publishes req and prints its address and use(address), how to use
// it, pinned by version and hash. arg is what the user typed, for the
// -handle hint.
func (d CommandDeps) send(ctx context.Context, confDir, arg string, req PublishRequest, use func(pinned string) string) error {
	if d.NewHubPublisher == nil {
		return fmt.Errorf("publishing is not available in this build")
	}
	publish, err := d.NewHubPublisher(confDir)
	if err != nil {
		return err
	}
	res, err := publish(ctx, req)
	var handle *HandleRequiredError
	if errors.As(err, &handle) {
		sub := "profiles"
		if req.Kind == "skill" {
			sub = "skills"
		}
		return fmt.Errorf("%w: clai %s publish %s -handle %s", err, sub, arg, handle.Suggested)
	}
	if err != nil {
		return err
	}
	pinned := fmt.Sprintf("%s@%d", res.Address, res.Version)
	what := ""
	if !res.Created {
		what = " (a new version)"
	}
	fmt.Fprintf(d.Out, "Published %s%s\n  %s\n  %s\n", pinned, what, res.URL, use(pinned+"#sha256="+res.SHA256))
	return nil
}

// noShellContext refuses a profile that names a shell context: a shell
// context is the runner's own, and clai never applies one from a fetched
// or installed profile, so publishing it would only mislead.
func noShellContext(body []byte) error {
	var p struct {
		ShellContext string `json:"shell_context"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return fmt.Errorf("read profile: %w", err)
	}
	if strings.TrimSpace(p.ShellContext) != "" {
		return fmt.Errorf("shell_context %q is your own: clai never applies a shell context from a shared profile, so remove it before you publish", p.ShellContext)
	}
	return nil
}

// pinnedSkills refuses a skill target that can change after publishing:
// everyone who trusts this version trusts its skills' bytes too.
func pinnedSkills(body []byte) error {
	skills, err := remote.ProfileSkills(body)
	if err != nil {
		return err
	}
	for _, t := range skills {
		if !t.Pinned() && t.SHA256 == "" {
			return fmt.Errorf("skill %s can change after you publish: pin it with @<version> (cchub:), @<ref> (gh:) or #sha256=<hex>", t)
		}
	}
	return nil
}

const publishHelp = `profiles publish <profile> [flags]. Publishes a local profile, by name or
path, to the Hub as your clai.chat account (clai cloud login first). The first
publish of a name creates the agent; publishing it again adds the next version.
The profile is checked like a remote one, and refused if it holds a credential.

Examples:
  clai profiles publish trade-bot
  clai profiles publish trade-bot -note 'tighter stop-loss'
  clai profiles publish ./bot.json -name trade-bot -handle lorentz`

func publishCommand(deps CommandDeps) *internal.Command {
	return newPublishCommand("profile", "Publish a profile to the Hub", publishHelp, deps.Publish)
}

// newPublishCommand builds `publish <what> [flags]` for profiles and skills.
func newPublishCommand(what, desc, help string, publish func(context.Context, string, PublishOptions) error) *internal.Command {
	var opts PublishOptions
	register := func(fs *flag.FlagSet) {
		fs.StringVar(&opts.Name, "name", "", "the name in its address (default: the "+what+"'s file or folder name)")
		fs.StringVar(&opts.Title, "title", "", "the title on the Hub (default: the name; kept on a new version)")
		fs.StringVar(&opts.Summary, "summary", "", "one line on what it does (kept on a new version)")
		fs.StringVar(&opts.Note, "note", "", "what changed in this version")
		fs.StringVar(&opts.Handle, "handle", "", "your handle, needed once on an account without one")
	}
	sub := &internal.Command{Name: "publish", Desc: desc, HelpText: help, Register: register}
	sub.OnRun = func(ctx context.Context, c *internal.Command) error {
		args := c.Args()[1:]
		if len(args) == 0 {
			return fmt.Errorf("usage: clai %s", strings.SplitN(help, ".", 2)[0])
		}
		// Flags may follow the argument, as they do in every example.
		if err := c.Flagset().Parse(args[1:]); err != nil {
			return err
		}
		if rest := c.Flagset().Args(); len(rest) > 0 {
			return fmt.Errorf("unexpected argument %q: publish takes one %s", rest[0], what)
		}
		return publish(ctx, args[0], opts)
	}
	return sub
}
