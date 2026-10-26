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

// remoteProfileResolver asks in the terminal only when stdin is one and
// the run is not -ni: a cron job, a pipe or a script never waits on a
// prompt; an untrusted target fails it with exit 3 instead.
func remoteProfileResolver(newResolver NewRemoteResolverFunc, confDir string, trustInput io.Reader, interactive bool) RemoteProfileResolver {
	return func(ctx context.Context, target string) (string, error) {
		if newResolver == nil {
			return "", fmt.Errorf("remote profile targets are not available in this build")
		}
		var prompt remote.Prompter
		if interactive {
			prompt = terminalTrustPrompt(trustInput, os.Stderr)
		}
		r, err := newResolver(confDir, prompt)
		if err != nil {
			return "", err
		}
		res, err := r.Resolve(ctx, target, remote.ConsentAsk)
		if err != nil {
			return "", err
		}
		return res.Path, nil
	}
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
	return fmt.Sprintf("fetched %s · sha256 %s\n\n%s\nRead it: %s\nTrust %s? [y/N] ",
		p.Target, p.Digest, p.Summary.String(), p.Page, p.Target)
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
	tConf.remoteProfile = true
	return nil
}
