package text

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/baalimago/clai/internal/remote"
	"github.com/baalimago/go_away_boilerplate/pkg/table"
)

// RemoteProfileResolver turns a remote -prp target into the local path of
// its trusted, cached bytes (architecture/remote-targets.md).
type RemoteProfileResolver func(ctx context.Context, target string) (string, error)

// NewRemoteResolverFunc builds the resolver for one run over confDir; a
// nil prompt means nobody can be asked to trust a new target.
type NewRemoteResolverFunc func(confDir string, prompt remote.Prompter) (remote.Resolver, error)

// RemoteSkillsResolver turns a profile's skill targets into the local
// roots the skill loader scans.
type RemoteSkillsResolver func(ctx context.Context, targets []string) ([]string, error)

// remoteRun resolves one run's remote profile and the skills it names. A
// profile the user just trusted at the prompt carries its skills with it,
// so they are not asked about one by one.
type remoteRun struct {
	newResolver NewRemoteResolverFunc
	confDir     string
	trustInput  io.Reader
	interactive bool
	consented   bool
}

// remoteProfileResolver asks in the terminal only when stdin is one and
// the run is not -ni: a cron job, a pipe or a script never waits on a
// prompt; an untrusted target fails it with exit 3 instead.
func remoteProfileResolver(newResolver NewRemoteResolverFunc, confDir string, trustInput io.Reader, interactive bool) RemoteProfileResolver {
	return (&remoteRun{newResolver: newResolver, confDir: confDir, trustInput: trustInput, interactive: interactive}).profile
}

func (r *remoteRun) resolver() (remote.Resolver, error) {
	if r.newResolver == nil {
		return remote.Resolver{}, fmt.Errorf("remote profile targets are not available in this build")
	}
	var prompt remote.Prompter
	if r.interactive {
		ask := terminalTrustPrompt(r.trustInput, os.Stderr)
		prompt = func(ctx context.Context, p remote.TrustPrompt) (bool, error) {
			ok, err := ask(ctx, p)
			r.consented = r.consented || ok
			return ok, err
		}
	}
	return r.newResolver(r.confDir, prompt)
}

func (r *remoteRun) profile(ctx context.Context, target string) (string, error) {
	res, err := r.resolver()
	if err != nil {
		return "", err
	}
	got, err := res.Resolve(ctx, target, remote.ConsentAsk)
	if err != nil {
		return "", err
	}
	return got.Path, nil
}

func (r *remoteRun) skills(ctx context.Context, targets []string) ([]string, error) {
	res, err := r.resolver()
	if err != nil {
		return nil, err
	}
	consent := remote.ConsentAsk
	if r.consented {
		consent = remote.ConsentGiven
	}
	resolved, err := res.ResolveSkills(ctx, targets, consent)
	if err != nil {
		return nil, err
	}
	roots := make([]string, 0, len(resolved))
	for _, s := range resolved {
		roots = append(roots, s.Root)
	}
	return roots, nil
}

// terminalAvailable reports whether someone can answer a prompt: stdin is
// a character device other than /dev/null, and the terminal the answer is
// read from (TTY, default /dev/tty) opens. Cron gives /dev/null and has
// no controlling terminal, so it never qualifies.
func terminalAvailable() bool {
	fi, err := os.Stdin.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 || isDevNull(fi) {
		return false
	}
	ttyPath := os.Getenv("TTY")
	if ttyPath == "" {
		ttyPath = "/dev/tty"
	}
	tty, err := os.Open(ttyPath)
	if err != nil {
		return false
	}
	defer tty.Close()
	ttyInfo, err := tty.Stat()
	return err == nil && !isDevNull(ttyInfo)
}

func isDevNull(fi os.FileInfo) bool {
	null, err := os.Stat(os.DevNull)
	return err == nil && os.SameFile(fi, null)
}

// terminalTrustPrompt asks on out, stderr in production, so a run whose
// stdout is piped still shows the question and keeps its output clean.
func terminalTrustPrompt(input io.Reader, out io.Writer) remote.Prompter {
	return func(_ context.Context, p remote.TrustPrompt) (bool, error) {
		if _, err := fmt.Fprint(out, FormatRemoteTrustPrompt(p)); err != nil {
			return false, fmt.Errorf("show trust prompt: %w", err)
		}
		answer, err := table.ReadUserInputFrom(input)
		if err != nil {
			return false, err
		}
		answer = strings.ToLower(strings.TrimSpace(answer))
		return answer == "y" || answer == "yes", nil
	}
}

// FormatRemoteTrustPrompt renders the question asked before a remote
// profile runs for the first time.
func FormatRemoteTrustPrompt(p remote.TrustPrompt) string {
	summary := ""
	if p.Summary != nil {
		summary = p.Summary.String()
	}
	return fmt.Sprintf("fetched %s · sha256 %s\n\n%s\nRead it: %s\nTrust %s? [y/N] ",
		p.Target, p.Digest, summary, p.Page, p.Target)
}

func resolveRemoteProfilePath(ctx context.Context, tConf *Configurations, resolve RemoteProfileResolver) error {
	if !remote.IsRemote(tConf.ProfilePath) {
		return nil
	}
	if resolve == nil {
		return fmt.Errorf("remote profile targets are not available here: %s", tConf.ProfilePath)
	}
	path, err := resolve(ctx, tConf.ProfilePath)
	if err != nil {
		return fmt.Errorf("profile %s: %w", tConf.ProfilePath, err)
	}
	tConf.ProfilePath = path
	return nil
}

// resolveProfileSkills fetches the skills the run's profile names; a run
// whose skills are off, or whose profile names none, fetches nothing.
func resolveProfileSkills(ctx context.Context, tConf *Configurations, resolve RemoteSkillsResolver) ([]string, error) {
	if !tConf.UseSkills || len(tConf.ProfileSkills) == 0 {
		return nil, nil
	}
	if resolve == nil {
		return nil, fmt.Errorf("profile skills are not available here: %s", strings.Join(tConf.ProfileSkills, ", "))
	}
	roots, err := resolve(ctx, tConf.ProfileSkills)
	if err != nil {
		return nil, fmt.Errorf("profile %s: %w", tConf.UseProfile, err)
	}
	return roots, nil
}
