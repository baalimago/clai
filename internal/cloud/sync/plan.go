package sync

import (
	"os"
	"slices"
	"strings"
)

// ChangeKind is what happened to a path on this machine since its base.
type ChangeKind string

const (
	Added    ChangeKind = "added"
	Modified ChangeKind = "modified"
	Deleted  ChangeKind = "deleted"
)

// LocalChange is one path a commit would send.
type LocalChange struct {
	Path    string     `json:"path"`
	Kind    ChangeKind `json:"kind"`
	SHA256  string     `json:"sha256,omitempty"`
	Size    int64      `json:"size"`
	BaseRev int64      `json:"-"`
}

// ExclusionKind says why a changed file stays out of a commit.
type ExclusionKind string

const (
	ExcludedSecret   ExclusionKind = "secret"
	ExcludedTooLarge ExclusionKind = "too_large"
	ExcludedImport   ExclusionKind = "import"
)

// Excluded is a changed file a commit leaves out, and why.
type Excluded struct {
	Path   string        `json:"path"`
	Kind   ExclusionKind `json:"kind"`
	Detail string        `json:"detail,omitempty"`
}

// Plan is a store's local side: what a commit would send, what it leaves
// out, and the conflicts that block it.
type Plan struct {
	Store     Store         `json:"store"`
	Changes   []LocalChange `json:"changes"`
	Excluded  []Excluded    `json:"excluded"`
	Conflicts []string      `json:"conflicts"`
	// Pulled is false until the store's first pull on this machine.
	Pulled bool `json:"pulled"`
	// Synced counts the files in agreement with the server.
	Synced int `json:"synced"`
}

// PlanOptions narrows a plan.
type PlanOptions struct {
	// Paths keeps only these files or directories ("profiles/").
	Paths []string
	// Imports includes conversations/imported/.
	Imports bool
}

func (o PlanOptions) wants(rel string) bool {
	if isImport(rel) && !o.Imports {
		return false
	}
	if len(o.Paths) == 0 {
		return true
	}
	for _, p := range o.Paths {
		p = strings.TrimSuffix(p, "/")
		if rel == p || strings.HasPrefix(rel, p+"/") {
			return true
		}
	}
	return false
}

// Plan reads the store's local side. It never touches the network.
func (e *Engine) Plan(store Store, opts PlanOptions) (Plan, error) {
	st, err := loadState(e.ConfDir, store)
	if err != nil {
		return Plan{}, err
	}
	plan, err := e.plan(store, st, opts)
	if err != nil {
		return Plan{}, err
	}
	return plan, st.save(e.ConfDir, store)
}

func (e *Engine) plan(store Store, st *state, opts PlanOptions) (Plan, error) {
	local, err := scan(e.ConfDir, store, st.Hashes)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{Store: store, Changes: []LocalChange{}, Excluded: []Excluded{}, Conflicts: []string{}, Pulled: st.Pulled}
	for p := range st.Conflicts {
		plan.Conflicts = append(plan.Conflicts, p)
	}
	slices.Sort(plan.Conflicts)
	paths := make([]string, 0, len(local))
	for p := range local {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	limit := FileLimits[store]
	for _, p := range paths {
		loc := local[p]
		base, ok := st.Entries[p]
		if ok && !base.Deleted && base.SHA256 == loc.SHA256 {
			plan.Synced++
			continue
		}
		if _, conflicted := st.Conflicts[p]; conflicted {
			continue
		}
		if isImport(p) && !opts.Imports {
			plan.Excluded = append(plan.Excluded, Excluded{Path: p, Kind: ExcludedImport})
			continue
		}
		if !opts.wants(p) {
			continue
		}
		if loc.Size > limit {
			plan.Excluded = append(plan.Excluded, Excluded{Path: p, Kind: ExcludedTooLarge, Detail: humanBytes(limit)})
			continue
		}
		if store == StoreConfig {
			b, err := os.ReadFile(e.abs(p))
			if err != nil {
				return Plan{}, err
			}
			if secret, found := FindSecret(p, b); found {
				plan.Excluded = append(plan.Excluded, Excluded{Path: p, Kind: ExcludedSecret, Detail: secret.String()})
				continue
			}
		}
		kind := Modified
		if !ok || base.Deleted {
			kind = Added
		}
		plan.Changes = append(plan.Changes, LocalChange{Path: p, Kind: kind, SHA256: loc.SHA256, Size: loc.Size, BaseRev: base.Rev})
	}
	gone := []string{}
	for p, base := range st.Entries {
		if _, ok := local[p]; ok || base.Deleted {
			continue
		}
		if _, conflicted := st.Conflicts[p]; conflicted || !opts.wants(p) {
			continue
		}
		gone = append(gone, p)
	}
	slices.Sort(gone)
	for _, p := range gone {
		plan.Changes = append(plan.Changes, LocalChange{Path: p, Kind: Deleted, BaseRev: st.Entries[p].Rev})
	}
	return plan, nil
}
