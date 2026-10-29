// Package remote resolves the profile targets -prp accepts besides a local
// file: cchub:<handle>/<name>[@n], gh:<owner>/<repo>[/path][@ref] and an
// https:// URL, each with an optional #sha256=<hex> suffix. A github.com
// blob or raw link and a raw.githubusercontent.com URL parse as gh:. It fetches,
// validates, caches and trust-checks the profile and hands back a local
// path the profile loader reads like any other.
// See architecture/remote-targets.md.
package remote

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Kind classifies a remote target by where it is fetched from.
type Kind int

const (
	KindHub Kind = iota + 1
	KindGitHub
	KindHTTPS
)

func (k Kind) String() string {
	switch k {
	case KindHub:
		return "cchub"
	case KindGitHub:
		return "gh"
	case KindHTTPS:
		return "https"
	}
	return "unknown"
}

// Target is one parsed remote profile target.
type Target struct {
	Kind Kind
	// Handle and Name address a hub agent; Version is 0 when unpinned.
	Handle  string
	Name    string
	Version int
	// Owner, Repo, Path and Ref address a file on GitHub; Ref is "" when
	// the target follows the default branch.
	Owner string
	Repo  string
	Path  string
	Ref   string
	// URL is the https target as written.
	URL string
	// SHA256 is the lower-case hex digest from a #sha256= suffix, or "".
	SHA256 string
}

// ErrNotRemote is returned by ParseTarget for anything -prp reads as a
// local path.
var ErrNotRemote = errors.New("not a remote profile target")

// DefaultGitHubPath is the file a gh: target without a path reads.
const DefaultGitHubPath = "agent.json"

var (
	hubPart   = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,78}[a-z0-9])?$`)
	ghOwner   = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)
	ghRepo    = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
	ghRef     = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,250}$`)
	sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// IsRemote reports whether a -prp value names a remote target rather than a
// local file. A local file is never fetched, whatever it is called.
func IsRemote(s string) bool {
	return strings.HasPrefix(s, "cchub:") || strings.HasPrefix(s, "gh:") ||
		strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://")
}

// ParseTarget parses a remote target. It returns ErrNotRemote for a local
// path and a descriptive error for a malformed remote one.
func ParseTarget(s string) (Target, error) {
	if !IsRemote(s) {
		return Target{}, ErrNotRemote
	}
	body, digest, err := splitDigest(s)
	if err != nil {
		return Target{}, fmt.Errorf("%s: %w", s, err)
	}
	var t Target
	switch {
	case strings.HasPrefix(body, "cchub:"):
		t, err = parseHub(strings.TrimPrefix(body, "cchub:"))
	case strings.HasPrefix(body, "gh:"):
		t, err = parseGitHub(strings.TrimPrefix(body, "gh:"))
	default:
		t, err = parseHTTPS(body)
	}
	if err != nil {
		return Target{}, fmt.Errorf("%s: %w", s, err)
	}
	t.SHA256 = digest
	return t, nil
}

func parseTypedTarget(s, ext, defaultFile string) (Target, error) {
	t, err := ParseTarget(s)
	if err != nil {
		return Target{}, err
	}
	if t.Kind != KindGitHub {
		return t, nil
	}
	addr, _, _ := strings.Cut(strings.TrimPrefix(s, "gh:"), "@")
	switch {
	case strings.Count(addr, "/") < 2:
		t.Path = defaultFile
	case !strings.HasSuffix(strings.ToLower(t.Path), ext):
		t.Path += "/" + defaultFile
	}
	return t, nil
}

func splitDigest(s string) (string, string, error) {
	body, fragment, found := strings.Cut(s, "#")
	if !found {
		return s, "", nil
	}
	digest, ok := strings.CutPrefix(fragment, "sha256=")
	if !ok {
		return "", "", fmt.Errorf("the only suffix a target takes is #sha256=<hex>, got #%s", fragment)
	}
	digest = strings.ToLower(digest)
	if !sha256Hex.MatchString(digest) {
		return "", "", fmt.Errorf("#sha256= needs 64 hex characters")
	}
	return body, digest, nil
}

func parseHub(s string) (Target, error) {
	addr, version, pinned := strings.Cut(s, "@")
	handle, name, ok := strings.Cut(addr, "/")
	if !ok || !hubPart.MatchString(handle) || len(handle) > 39 || len(handle) < 2 || !hubPart.MatchString(name) {
		return Target{}, fmt.Errorf("a cchub target is cchub:<handle>/<name>[@<version>]")
	}
	t := Target{Kind: KindHub, Handle: handle, Name: name}
	if pinned {
		n, err := strconv.Atoi(version)
		if err != nil || n < 1 || strconv.Itoa(n) != version {
			return Target{}, fmt.Errorf("a hub version is a whole number from 1, got %q", version)
		}
		t.Version = n
	}
	return t, nil
}

func parseGitHub(s string) (Target, error) {
	addr, ref, pinned := strings.Cut(s, "@")
	parts := strings.Split(addr, "/")
	if len(parts) < 2 || !ghOwner.MatchString(parts[0]) || !ghRepo.MatchString(parts[1]) {
		return Target{}, fmt.Errorf("a GitHub target is gh:<owner>/<repo>[/<path>][@<ref>]")
	}
	// raw.githubusercontent.com resolves a . or .. segment, so either would
	// fetch from somewhere other than the target reads as.
	if dotSegment(parts[0]) || dotSegment(parts[1]) {
		return Target{}, fmt.Errorf(`a GitHub owner or repo cannot be "." or ".."`)
	}
	t := Target{Kind: KindGitHub, Owner: parts[0], Repo: parts[1], Path: DefaultGitHubPath}
	if len(parts) > 2 {
		if hasEmptyOrDotSegment(parts[2:]) {
			return Target{}, fmt.Errorf("the path in a GitHub target cannot hold an empty, . or .. segment")
		}
		t.Path = strings.Join(parts[2:], "/")
	}
	if pinned {
		if !ghRef.MatchString(ref) || strings.Contains(ref, "..") {
			return Target{}, fmt.Errorf("%q is not a branch, tag or commit", ref)
		}
		if hasEmptyOrDotSegment(strings.Split(ref, "/")) {
			return Target{}, fmt.Errorf("the ref %q cannot hold an empty, . or .. segment", ref)
		}
		t.Ref = ref
	}
	return t, nil
}

func dotSegment(seg string) bool { return seg == "." || seg == ".." }

func hasEmptyOrDotSegment(segs []string) bool {
	for _, seg := range segs {
		if seg == "" || dotSegment(seg) {
			return true
		}
	}
	return false
}

func parseHTTPS(s string) (Target, error) {
	u, err := url.Parse(s)
	if err != nil {
		return Target{}, fmt.Errorf("parse url: %w", err)
	}
	if u.Scheme != "https" {
		return Target{}, fmt.Errorf("only https:// is fetched; a profile over plain http could be changed in transit")
	}
	if u.Host == "" || u.User != nil {
		return Target{}, fmt.Errorf("an https target needs a host and no user info")
	}
	// A server resolves a . or .. segment, so the target would read as one
	// path and fetch another.
	if slices.ContainsFunc(strings.Split(u.Path, "/"), dotSegment) {
		return Target{}, fmt.Errorf("an https target's path cannot hold a . or .. segment")
	}
	if gh, ok := gitHubFileURL(u); ok {
		return parseGitHub(gh)
	}
	return Target{Kind: KindHTTPS, URL: s}, nil
}

// gitHubFileURL reads a link to a file on GitHub as the gh: target for it,
// so a copied link works: github.com/<owner>/<repo>/blob|raw/<ref>/<path>
// would answer a page or a redirect, and raw.githubusercontent.com answers
// text/plain, which an https target refuses. The ref is the first segment
// after blob or raw (or a refs/heads/<branch> or refs/tags/<tag>), so a
// branch with a / in it needs the gh: form.
func gitHubFileURL(u *url.URL) (string, bool) {
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	switch strings.ToLower(u.Host) {
	case "github.com", "www.github.com":
		if len(parts) < 5 || (parts[2] != "blob" && parts[2] != "raw") {
			return "", false
		}
		parts = append(parts[:2], parts[3:]...)
	case "raw.githubusercontent.com":
		if len(parts) < 4 {
			return "", false
		}
	default:
		return "", false
	}
	// GitHub's Raw button links refs/heads/<branch> and refs/tags/<tag>.
	refLen := 1
	if parts[2] == "refs" && len(parts) >= 6 && (parts[3] == "heads" || parts[3] == "tags") {
		refLen = 3
	}
	owner, repo := parts[0], parts[1]
	ref, path := strings.Join(parts[2:2+refLen], "/"), strings.Join(parts[2+refLen:], "/")
	if ref == "HEAD" {
		return owner + "/" + repo + "/" + path, true
	}
	return owner + "/" + repo + "/" + path + "@" + ref, true
}

// Pinned reports whether the target names fixed bytes: a hub version or a
// GitHub ref. A pinned target that is cached is never fetched again.
func (t Target) Pinned() bool {
	switch t.Kind {
	case KindHub:
		return t.Version > 0
	case KindGitHub:
		return t.Ref != ""
	}
	return false
}

// Base is the target without its version or ref and without a digest.
// Trust is recorded per base and content hash, so pinning a version that
// was already trusted does not ask again.
func (t Target) Base() string {
	switch t.Kind {
	case KindHub:
		return fmt.Sprintf("cchub:%s/%s", t.Handle, t.Name)
	case KindGitHub:
		s := fmt.Sprintf("gh:%s/%s", t.Owner, t.Repo)
		if t.Path != DefaultGitHubPath {
			s += "/" + t.Path
		}
		return s
	}
	return t.URL
}

// String is the target as it would be written, without a digest.
func (t Target) String() string {
	switch t.Kind {
	case KindHub:
		if t.Version > 0 {
			return fmt.Sprintf("%s@%d", t.Base(), t.Version)
		}
	case KindGitHub:
		if t.Ref != "" {
			return t.Base() + "@" + t.Ref
		}
	}
	return t.Base()
}

// WithDigest is the target with a #sha256= suffix, as a stamp records it.
func (t Target) WithDigest(digest string) string {
	return t.String() + "#sha256=" + digest
}

// Unpinned is the target that follows the newest version: a hub target
// without its version. Other kinds have no newer version to follow, so
// they are returned unchanged.
func (t Target) Unpinned() Target {
	t.SHA256 = ""
	if t.Kind == KindHub {
		t.Version = 0
	}
	return t
}

// FetchURL is the address the target's bytes are read from. hubBase is
// the clai cloud origin a cchub: target resolves against.
func (t Target) FetchURL(hubBase string) string {
	switch t.Kind {
	case KindHub:
		base := strings.TrimRight(hubBase, "/")
		if t.Version > 0 {
			return fmt.Sprintf("%s/hub/%s/%s/v/%d/raw", base, t.Handle, t.Name, t.Version)
		}
		return fmt.Sprintf("%s/hub/%s/%s/raw", base, t.Handle, t.Name)
	case KindGitHub:
		ref := t.Ref
		if ref == "" {
			ref = "HEAD"
		}
		return fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s", t.Owner, t.Repo, ref, t.Path)
	}
	return t.URL
}

// PageURL is where a person reads the target before trusting it.
func (t Target) PageURL(hubBase string) string {
	switch t.Kind {
	case KindHub:
		return fmt.Sprintf("%s/hub/%s/%s/", strings.TrimRight(hubBase, "/"), t.Handle, t.Name)
	case KindGitHub:
		ref := t.Ref
		if ref == "" {
			ref = "HEAD"
		}
		return fmt.Sprintf("https://github.com/%s/%s/blob/%s/%s", t.Owner, t.Repo, ref, t.Path)
	}
	return t.URL
}

// DefaultName is the profile name `clai profiles install` uses when none is
// given: the hub name, the GitHub file's base name (the repo for
// agent.json), or the URL's last path segment.
func (t Target) DefaultName() string {
	var name string
	switch t.Kind {
	case KindHub:
		name = t.Name
	case KindGitHub:
		name = lastSegment(t.Path)
		if t.Path == DefaultGitHubPath {
			name = t.Repo
		}
	default:
		u, err := url.Parse(t.URL)
		if err == nil {
			name = lastSegment(u.Path)
		}
	}
	name = strings.TrimSuffix(name, ".json")
	if name == "" {
		return "remote"
	}
	return name
}

func lastSegment(p string) string {
	p = strings.TrimRight(p, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
