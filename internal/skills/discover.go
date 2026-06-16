package skills

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

func Discover(opts Options) (*Manager, error) {
	log := newLogger(opts.LogLevel)
	var (
		cfg Config
		err error
	)
	if opts.ConfigOverride != nil {
		cfg = withConfigDefaults(*opts.ConfigOverride)
	} else {
		cfg, err = LoadConfig(opts.ConfigDir)
		if err != nil {
			return nil, fmt.Errorf("load skills config: %w", err)
		}
	}
	if opts.WorkingDir == "" {
		opts.WorkingDir, _ = os.Getwd()
	}
	roots := discoverRoots(opts.WorkingDir, opts.ConfigDir, cfg)
	precedence := make(precedenceMap, len(roots))
	for i, root := range roots {
		if _, exists := precedence[root.path]; !exists {
			precedence[root.path] = i
		}
	}
	candidates := make([]Candidate, 0)
	invalids := make([]InvalidSkill, 0)
	sources := make([]SourceSummary, 0, len(roots))
	for _, root := range roots {
		summary := SourceSummary{Class: root.class, Path: root.path}
		found, bad, err := scanRoot(root.class, root.path)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, found...)
		invalids = append(invalids, bad...)
		summary.Loaded = len(found)
		summary.Invalid = len(bad)
		if debugSkills() {
			log.Warnf("skills %s: %s [loaded=%d invalid=%d]", root.class, root.path, summary.Loaded, summary.Invalid)
		}
		sources = append(sources, summary)
	}
	resolution := resolveCandidates(candidates, invalids, precedence)
	loadedByRoot := map[string]int{}
	for _, skill := range resolution.Active {
		loadedByRoot[skill.SourceRoot]++
	}
	if opts.LogQueryText && len(resolution.Active) > 0 {
		for _, summary := range sources {
			if loaded := loadedByRoot[summary.Path]; loaded > 0 {
				log.Infof("skills %s: %s [loaded=%d]", summary.Class, summary.Path, loaded)
			}
		}
		log.Infof("skills: loaded=%d shadowed=%d invalid=%d", len(resolution.Active), len(resolution.Shadowed), len(invalids))
	}
	return &Manager{
		Config:         cfg,
		Summary:        Summary{Loaded: len(resolution.Active), Shadowed: len(resolution.Shadowed), Invalid: len(invalids), Sources: sources, Invalids: invalids, ShadowedSkills: resolution.Shadowed},
		Skills:         resolution.Active,
		cacheDir:       opts.CacheDir,
		trustPrompter:  opts.TrustPrompter,
		knownToolNames: toSet(opts.KnownToolNames),
		logger:         log,
		state: ActivationState{
			Allowed:    map[string]struct{}{},
			Disallowed: map[string]struct{}{},
		},
	}, nil
}

type Manager struct {
	Config  Config
	Summary Summary
	Skills  map[string]Skill

	cacheDir       string
	trustPrompter  func(context.Context, TrustPrompt) (bool, error)
	knownToolNames map[string]struct{}
	state          ActivationState
	logger         logger
}

type rootSpec struct {
	class string
	path  string
}

func discoverRoots(workingDir, configDir string, cfg Config) []rootSpec {
	roots := []rootSpec{{class: "default", path: filepath.Join(configDir, "skills")}}
	for _, dir := range cfg.GlobalSkillDirs {
		if dir == "" {
			continue
		}
		roots = append(roots, rootSpec{class: "global", path: expandHome(dir)})
	}
	for current := workingDir; ; current = filepath.Dir(current) {
		for _, rel := range cfg.ProjectSkillDirs {
			if rel == "" {
				continue
			}
			roots = append(roots, rootSpec{class: "project", path: filepath.Join(current, rel)})
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}
	return roots
}

func scanRoot(class, root string) ([]Candidate, []InvalidSkill, error) {
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("scan skill root %q: %w", root, err)
	}
	slices.SortFunc(entries, func(a, b os.DirEntry) int { return cmpString(a.Name(), b.Name()) })
	candidates := make([]Candidate, 0)
	invalids := make([]InvalidSkill, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		skill, invalid := parseSkill(class, root, dir)
		if invalid != nil {
			invalids = append(invalids, *invalid)
			continue
		}
		candidates = append(candidates, Candidate{Skill: skill})
	}
	return candidates, invalids, nil
}
