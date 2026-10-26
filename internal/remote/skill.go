package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/baalimago/clai/internal/skills"
)

// SkillFile is the file a skill target without one names.
const SkillFile = "SKILL.md"

var skillName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ParseSkillTarget parses a skill target: a cchub: skill, a gh: SKILL.md
// (a path that is not a .md file names its directory), or an https URL.
func ParseSkillTarget(s string) (Target, error) {
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
		t.Path = SkillFile
	case !strings.HasSuffix(strings.ToLower(t.Path), ".md"):
		t.Path += "/" + SkillFile
	}
	return t, nil
}

// InvalidSkillError is a skill target whose bytes clai would not load.
type InvalidSkillError struct {
	Target string
	Err    error
}

func (e *InvalidSkillError) Error() string {
	return fmt.Sprintf("%s is not a skill clai can load: %v", e.Target, e.Err)
}

func (e *InvalidSkillError) Unwrap() error { return e.Err }

// ExitCode marks a refused skill apart from a failed run.
func (e *InvalidSkillError) ExitCode() int { return ExitRefused }

// SkillSummary is what the trust prompt shows of a skill.
type SkillSummary struct {
	Name        string
	Description string
}

func (s SkillSummary) String() string {
	return fmt.Sprintf("This skill is instructions the agent may load:\n  %s: %s\n", s.Name, s.Description)
}

// ResolvedSkill is a skill target ready to load: Root holds one directory,
// Name, with its SKILL.md.
type ResolvedSkill struct {
	Target      Target
	Name        string
	Description string
	Digest      string
	Root        string
	// Fetched is false when the bytes came from the cache.
	Fetched bool
}

// ResolveSkill turns a skill target into a trusted skill directory. It is
// trusted the way a profile is: per base and hash, asked once.
func (r Resolver) ResolveSkill(ctx context.Context, raw string, consent Consent) (ResolvedSkill, error) {
	t, err := ParseSkillTarget(raw)
	if err != nil {
		return ResolvedSkill{}, err
	}
	body, fetched, err := r.read(ctx, t, skillDoc)
	if err != nil {
		return ResolvedSkill{}, err
	}
	if len(body) > MaxBodyBytes {
		return ResolvedSkill{}, &InvalidSkillError{Target: t.String(), Err: fmt.Errorf("larger than %d KiB", MaxBodyBytes/1024)}
	}
	meta, err := skills.CheckSkill(body)
	if err != nil {
		return ResolvedSkill{}, &InvalidSkillError{Target: t.String(), Err: err}
	}
	name := meta.Name
	if !skillName.MatchString(name) {
		name = t.skillDefaultName()
	}
	digest := Digest(body)
	if t.SHA256 != "" && t.SHA256 != digest {
		return ResolvedSkill{}, &HashMismatchError{Target: t.String(), Want: t.SHA256, Got: digest}
	}
	summary := SkillSummary{Name: name, Description: meta.Description}
	if err := r.authorize(ctx, t, digest, summary, consent); err != nil {
		return ResolvedSkill{}, err
	}
	if _, err := r.Cache.Store(t.String(), body); err != nil {
		return ResolvedSkill{}, err
	}
	root, err := r.Cache.skillRoot(digest, name, body)
	if err != nil {
		return ResolvedSkill{}, err
	}
	r.noteUnpinned(t, fetched)
	return ResolvedSkill{Target: t, Name: name, Description: meta.Description, Digest: digest, Root: root, Fetched: fetched}, nil
}

// ResolveSkills resolves every target in order and stops at the first
// that fails, naming it.
func (r Resolver) ResolveSkills(ctx context.Context, targets []string, consent Consent) ([]ResolvedSkill, error) {
	out := make([]ResolvedSkill, 0, len(targets))
	for _, target := range targets {
		s, err := r.ResolveSkill(ctx, target, consent)
		if err != nil {
			return nil, fmt.Errorf("skill %s: %w", target, err)
		}
		out = append(out, s)
	}
	return out, nil
}

// skillDefaultName names a skill whose frontmatter does not: the hub name,
// or the directory that holds its SKILL.md.
func (t Target) skillDefaultName() string {
	p := t.Path
	if t.Kind == KindHub {
		return t.Name
	}
	if t.Kind == KindHTTPS {
		if u, err := url.Parse(t.URL); err == nil {
			p = u.Path
		}
	}
	dir := lastSegment(strings.TrimSuffix(p, "/"+lastSegment(p)))
	if strings.HasSuffix(strings.ToLower(lastSegment(p)), ".md") && !strings.EqualFold(lastSegment(p), SkillFile) {
		dir = strings.TrimSuffix(lastSegment(p), ".md")
	}
	if !skillName.MatchString(dir) {
		if t.Kind == KindGitHub {
			return t.Repo
		}
		return "remote-skill"
	}
	return dir
}

// skillRoot lays out <cache>/remote/skills/<digest>/<name>/SKILL.md, the
// shape the skill loader scans, and returns the root.
func (c Cache) skillRoot(digest, name string, body []byte) (string, error) {
	root := filepath.Join(c.dir, "skills", digest)
	path := filepath.Join(root, name, SkillFile)
	b, err := os.ReadFile(path)
	if err == nil && Digest(b) == digest {
		return root, nil
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("read cached skill %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create cached skill: %w", err)
	}
	if err := writeFileAtomic(path, body); err != nil {
		return "", err
	}
	return root, nil
}

// ProfileSkills reads the skill targets a profile's "skills" names.
func ProfileSkills(body []byte) ([]Target, error) {
	var p struct {
		Skills json.RawMessage `json:"skills"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("read profile skills: %w", err)
	}
	if p.Skills == nil {
		return nil, nil
	}
	return skillTargets(p.Skills)
}

func skillTargets(raw json.RawMessage) ([]Target, error) {
	var list []string
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("skills: needs a list of skill targets")
	}
	out := make([]Target, 0, len(list))
	for _, s := range list {
		t, err := ParseSkillTarget(s)
		if errors.Is(err, ErrNotRemote) {
			return nil, fmt.Errorf("skills: %q is not a skill target (cchub:<handle>/<skill>, gh:<owner>/<repo>[/<path>] or https://)", s)
		}
		if err != nil {
			return nil, fmt.Errorf("skills: %w", err)
		}
		out = append(out, t)
	}
	return out, nil
}
