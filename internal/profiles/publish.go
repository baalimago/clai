package profiles

import (
	"context"
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
	if d.FindSecret != nil {
		if where, found := d.FindSecret(path, body); found {
			return fmt.Errorf("%s holds what looks like a credential (%s): move it to an envfile or an environment variable, then publish", path, where)
		}
	}
	if d.NewHubPublisher == nil {
		return fmt.Errorf("publishing is not available in this build")
	}
	publish, err := d.NewHubPublisher(confDir)
	if err != nil {
		return err
	}
	res, err := publish(ctx, PublishRequest{
		Name: name, Body: string(body), Title: opts.Title, Summary: opts.Summary, Note: opts.Note, Handle: opts.Handle,
	})
	var handle *HandleRequiredError
	if errors.As(err, &handle) {
		return fmt.Errorf("%w: clai profiles publish %s -handle %s", err, profile, handle.Suggested)
	}
	if err != nil {
		return err
	}
	pinned := fmt.Sprintf("%s@%d", res.Address, res.Version)
	what := ""
	if !res.Created {
		what = " (a new version)"
	}
	fmt.Fprintf(d.Out, "Published %s%s\n  %s\n  clai -prp %s#sha256=%s q …\n", pinned, what, res.URL, pinned, res.SHA256)
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
	var opts PublishOptions
	register := func(fs *flag.FlagSet) {
		fs.StringVar(&opts.Name, "name", "", "the agent's name in its address (default: the profile's file name)")
		fs.StringVar(&opts.Title, "title", "", "the title on the Hub (default: the name; kept on a new version)")
		fs.StringVar(&opts.Summary, "summary", "", "one line on what it does (kept on a new version)")
		fs.StringVar(&opts.Note, "note", "", "what changed in this version")
		fs.StringVar(&opts.Handle, "handle", "", "your handle, needed once on an account without one")
	}
	sub := &internal.Command{Name: "publish", Desc: "Publish a profile to the Hub", HelpText: publishHelp, Register: register}
	sub.OnRun = func(ctx context.Context, c *internal.Command) error {
		args := c.Args()[1:]
		if len(args) == 0 {
			return fmt.Errorf("usage: clai profiles publish <profile> [flags]")
		}
		// Flags may follow the profile, as they do in every example.
		if err := c.Flagset().Parse(args[1:]); err != nil {
			return err
		}
		if rest := c.Flagset().Args(); len(rest) > 0 {
			return fmt.Errorf("unexpected argument %q: publish takes one profile", rest[0])
		}
		return deps.Publish(ctx, args[0], opts)
	}
	return sub
}
