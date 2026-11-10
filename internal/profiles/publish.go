package profiles

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/profile"
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
	// Mode picks which mode config's alias to publish: text, photo, audio,
	// or the index among the modes that define the alias.
	Mode string
	// In answers selection prompts; nil means nobody can be asked.
	In io.Reader
}

var hubName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,58}[a-z0-9])?$`)

// Publish sends a local profile, by name or path, to the Hub. It is checked
// as strictly as a remote profile and for credentials before it leaves.
func (d CommandDeps) Publish(ctx context.Context, ref string, opts PublishOptions) error {
	d = d.withEnv(ctx)
	confDir, err := d.configDir(ctx)
	if err != nil {
		return err
	}
	path := ref
	if !strings.ContainsRune(ref, os.PathSeparator) && !strings.HasSuffix(ref, ".json") {
		path = filepath.Join(confDir, "profiles", ref+".json")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return diag.Errorf("read profile %s: %w", ref, err)
	}
	name := opts.Name
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(path), ".json")
	}
	if !hubName.MatchString(name) {
		return fmt.Errorf("%q cannot be a Hub name (lower-case letters, digits and dashes); pass one with -name", name)
	}
	env, err := d.profileEnv(confDir)
	if err != nil {
		return err
	}
	// Publish has no tool registry: a name is the Hub's to resolve.
	env.ToolNames = nil
	findings, err := profile.Validate(body, profile.Publish, env)
	if err != nil {
		return diag.Errorf("check %s: %w", path, err)
	}
	if problems := findings.Problems(profile.Publish); len(problems) > 0 {
		// Clause 7 gives a refused body one status on every route, 1; a run's
		// remote refusal keeps its own 3 in remote.Resolver.
		return fmt.Errorf("%s is not a valid profile:\n  %s", path, strings.Join(problems, "\n  "))
	}
	if d.FindSecret != nil {
		if where, found := d.FindSecret(path, body); found {
			return fmt.Errorf("%s holds what looks like a credential (%s): move it to an envfile or an environment variable, then publish", path, where)
		}
	}
	return d.send(ctx, confDir, ref, PublishRequest{
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
	if handle, ok := errors.AsType[*HandleRequiredError](err); ok {
		sub := "profiles publish"
		switch req.Kind {
		case "skill":
			sub = "skills publish"
		case "alias":
			sub = "cloud publish alias"
		}
		return diag.Errorf("%w: clai %s %s -handle %s", err, sub, arg, handle.Suggested)
	}
	if err != nil {
		return err
	}
	pinned := fmt.Sprintf("%s@%d", res.Address, res.Version)
	what := ""
	if !res.Created {
		what = " (a new version)"
	}
	fmt.Fprintf(d.out(), "Published %s%s\n  %s\n  %s\n", pinned, what, res.URL, use(pinned+"#sha256="+res.SHA256))
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
