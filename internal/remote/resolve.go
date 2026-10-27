package remote

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/baalimago/clai/internal/look"
)

// ExitRefused is the exit status of a run that refused a remote profile:
// untrusted, invalid, not JSON, or not matching its #sha256=. A script
// can tell it apart from a failed run (1).
const ExitRefused = 3

// Consent says whether the caller's own command already is the user's
// consent to run the bytes it resolves.
type Consent int

const (
	// ConsentAsk trusts a hash already in the trust store, or a matching
	// #sha256=, and otherwise asks, or refuses where nobody can be asked.
	ConsentAsk Consent = iota + 1
	// ConsentGiven trusts whatever is fetched: `clai profiles trust` and
	// `clai profiles install` are the user saying so.
	ConsentGiven
)

// TrustPrompt is what the user is shown before trusting a target: a
// profile's Summary or a skill's SkillSummary.
type TrustPrompt struct {
	Target  string
	Digest  string
	Page    string
	Summary Section
}

// Section is what a trust prompt shows of a target: its lines in clai's
// look, and the same without colour as String.
type Section interface {
	fmt.Stringer
	Lines(look.Style) []string
}

// Prompter asks the user whether to trust a target. A nil Prompter means
// there is no terminal to ask in.
type Prompter func(context.Context, TrustPrompt) (bool, error)

// UntrustedError is a target whose bytes the user has not trusted.
type UntrustedError struct {
	Target  string
	Digest  string
	Page    string
	Asked   bool
	Changed bool
}

func (e *UntrustedError) Error() string {
	if e.Asked {
		return fmt.Sprintf("%s was not trusted. Not running it", e.Target)
	}
	what := "is a profile"
	if e.Changed {
		what = "changed to a version"
	}
	return fmt.Sprintf("%s %s (sha256 %s) you have not trusted, and there is no terminal to ask in. Not running it.\n"+
		"  Read it:  %s\n"+
		"  Trust it: clai profiles trust %s\n"+
		"  Or pin it with its hash: %s#sha256=%s",
		e.Target, what, shortDigest(e.Digest), e.Page, e.Target, e.Target, e.Digest)
}

func (e *UntrustedError) ExitCode() int { return ExitRefused }

// HashMismatchError is a target whose bytes do not match its #sha256=.
type HashMismatchError struct {
	Target string
	Want   string
	Got    string
}

func (e *HashMismatchError) Error() string {
	return fmt.Sprintf("%s does not match its #sha256=: want %s, got %s. Not running it", e.Target, shortDigest(e.Want), shortDigest(e.Got))
}

func (e *HashMismatchError) ExitCode() int { return ExitRefused }

// OfflineError is a target that is not cached while fetching is off.
type OfflineError struct{ Target string }

func (e *OfflineError) Error() string {
	return fmt.Sprintf("%s is not cached, and CLAI_CLOUD=off allows no fetch", e.Target)
}

func (e *OfflineError) ExitCode() int { return ExitRefused }

func shortDigest(d string) string {
	if len(d) < 12 {
		return d
	}
	return d[:4] + "…" + d[len(d)-4:]
}

// Resolver fetches, validates, trusts and caches remote profile targets.
type Resolver struct {
	Client Doer
	// HubBase is the clai cloud origin cchub: targets resolve against.
	HubBase string
	// Offline makes every target cache-only (CLAI_CLOUD=off).
	Offline bool
	Trust   TrustStore
	Cache   Cache
	Prompt  Prompter
	Now     func() time.Time
	// Note receives advice that does not stop the run, such as an
	// unpinned gh: target following its default branch.
	Note func(string)
}

// Resolved is a target ready to load.
type Resolved struct {
	Target  Target
	Path    string
	Digest  string
	Body    []byte
	Summary Summary
	// Fetched is false when the bytes came from the cache.
	Fetched bool
}

// Resolve turns a remote target into a trusted local file.
func (r Resolver) Resolve(ctx context.Context, raw string, consent Consent) (Resolved, error) {
	t, err := ParseTarget(raw)
	if err != nil {
		return Resolved{}, err
	}
	body, fetched, err := r.read(ctx, t, profileDoc)
	if err != nil {
		return Resolved{}, err
	}
	checked, err := Validate(t.String(), body)
	if err != nil {
		return Resolved{}, err
	}
	summary, err := Summarize(body, checked)
	if err != nil {
		return Resolved{}, err
	}
	digest := Digest(body)
	if t.SHA256 != "" && t.SHA256 != digest {
		return Resolved{}, &HashMismatchError{Target: t.String(), Want: t.SHA256, Got: digest}
	}
	if err := r.authorize(ctx, t, digest, summary, consent); err != nil {
		return Resolved{}, err
	}
	if _, err := r.Cache.Store(t.String(), body); err != nil {
		return Resolved{}, err
	}
	r.noteUnpinned(t, fetched)
	return Resolved{Target: t, Path: r.Cache.Path(digest), Digest: digest, Body: body, Summary: summary, Fetched: fetched}, nil
}

func (r Resolver) noteUnpinned(t Target, fetched bool) {
	if fetched && t.Kind == KindGitHub && t.Ref == "" && r.Note != nil {
		r.Note(fmt.Sprintf("%s has no @ref, so it follows the default branch. Pin a tag or commit for scheduled runs.", t))
	}
}

// docKind is what a target is expected to hold.
type docKind int

const (
	profileDoc docKind = iota + 1
	skillDoc
)

// read returns cached bytes for a pinned target, a target whose hash is
// given and cached, or any target while offline; otherwise it fetches.
func (r Resolver) read(ctx context.Context, t Target, doc docKind) ([]byte, bool, error) {
	if t.Pinned() || r.Offline {
		body, digest, err := r.Cache.Lookup(t.String())
		switch {
		case err == nil && (t.SHA256 == "" || t.SHA256 == digest):
			return body, false, nil
		case err != nil && !errors.Is(err, ErrNotCached):
			return nil, false, err
		}
	}
	if r.Offline {
		return nil, false, &OfflineError{Target: t.String()}
	}
	accept, requireJSON := "application/json", t.Kind != KindGitHub
	if doc == skillDoc {
		accept, requireJSON = "text/markdown, text/plain", false
	}
	body, err := fetch(ctx, r.Client, t.String(), t.FetchURL(r.HubBase), accept, requireJSON)
	if err != nil {
		return nil, false, err
	}
	return body, true, nil
}

func (r Resolver) authorize(ctx context.Context, t Target, digest string, summary Section, consent Consent) error {
	base := t.Base()
	trusted, err := r.Trust.Trusted(base, digest)
	if err != nil {
		return err
	}
	if trusted {
		return nil
	}
	if t.SHA256 == "" && consent != ConsentGiven {
		if r.Prompt == nil {
			changed, err := r.hasTrustedOther(base)
			if err != nil {
				return err
			}
			return &UntrustedError{Target: t.String(), Digest: digest, Page: t.PageURL(r.HubBase), Changed: changed}
		}
		ok, err := r.Prompt(ctx, TrustPrompt{Target: t.String(), Digest: digest, Page: t.PageURL(r.HubBase), Summary: summary})
		if err != nil {
			return fmt.Errorf("ask to trust %s: %w", t, err)
		}
		if !ok {
			return &UntrustedError{Target: t.String(), Digest: digest, Asked: true}
		}
	}
	return r.Trust.Trust(base, digest, r.now())
}

func (r Resolver) hasTrustedOther(base string) (bool, error) {
	rec, err := r.Trust.load()
	if err != nil {
		return false, err
	}
	return len(rec.Targets[base]) > 0, nil
}

func (r Resolver) now() time.Time {
	if r.Now == nil {
		return time.Now()
	}
	return r.Now()
}
