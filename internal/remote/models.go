package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/baalimago/clai/internal/utils"
)

// ModelListFile is the file a GitHub model-list target without a .json path names.
const ModelListFile = "models.json"

// ParseModelListTarget parses a remote model-list target: a cchub: alias list,
// a gh: models.json (or any .json file), or an https URL.
func ParseModelListTarget(s string) (Target, error) {
	return parseTypedTarget(s, ".json", ModelListFile)
}

// InvalidModelListError reports every problem found in a remote model list.
type InvalidModelListError struct {
	Target   string
	Problems []string
}

func (e *InvalidModelListError) Error() string {
	return fmt.Sprintf("%s is not a valid model list:\n  %s", e.Target, strings.Join(e.Problems, "\n  "))
}

// ExitCode marks a refused model list apart from a failed run.
func (e *InvalidModelListError) ExitCode() int { return ExitRefused }

// ValidateModelList checks a remote model list strictly and returns its
// deduplicated model names in first-seen order.
func ValidateModelList(target string, body []byte) ([]string, error) {
	invalid := func(problems ...string) error {
		return &InvalidModelListError{Target: target, Problems: problems}
	}
	if len(body) > MaxBodyBytes {
		return nil, invalid(fmt.Sprintf("larger than %d KiB", MaxBodyBytes/1024))
	}
	var list []string
	dec := json.NewDecoder(bytes.NewReader(body))
	if err := dec.Decode(&list); err != nil || list == nil {
		return nil, invalid("not a JSON array of model names")
	}
	if dec.More() {
		return nil, invalid("not one JSON array: more follows the first")
	}
	var problems []string
	seen := make(map[string]bool, len(list))
	out := make([]string, 0, len(list))
	for i, raw := range list {
		m := strings.TrimSpace(raw)
		switch {
		case m == "":
			problems = append(problems, fmt.Sprintf("model[%d]: is blank", i))
		case IsRemote(m):
			problems = append(problems, fmt.Sprintf("model[%d]: %q is a remote target, not a concrete model name", i, Printable(m)))
		case Printable(m) != m:
			problems = append(problems, fmt.Sprintf("model[%d]: %q holds control characters", i, Printable(m)))
		case !seen[m]:
			seen[m] = true
			out = append(out, m)
		}
	}
	if len(problems) == 0 && len(out) == 0 {
		problems = append(problems, "lists no models")
	}
	if len(problems) > 0 {
		return nil, invalid(problems...)
	}
	return out, nil
}

// ResolvedModelList is a remote model list ready to try in order.
type ResolvedModelList struct {
	Target Target
	Models []string
	Digest string
	// Fetched is false when the bytes came from the cache.
	Fetched bool
}

// ResolveModelList fetches, validates and caches a remote model list. Model
// names are inert strings matched against local vendor configurations, so no
// interactive trust prompt or trust.json entry is required; an explicit
// #sha256= suffix is still verified.
func (r Resolver) ResolveModelList(ctx context.Context, raw string) (ResolvedModelList, error) {
	t, err := ParseModelListTarget(raw)
	if err != nil {
		return ResolvedModelList{}, err
	}
	body, fetched, err := r.read(ctx, t, modelListDoc)
	if err != nil {
		return ResolvedModelList{}, err
	}
	models, err := ValidateModelList(t.String(), body)
	if err != nil {
		return ResolvedModelList{}, err
	}
	digest := Digest(body)
	if t.SHA256 != "" && t.SHA256 != digest {
		return ResolvedModelList{}, &HashMismatchError{Target: t.String(), Want: t.SHA256, Got: digest}
	}
	if _, err := r.Cache.Store(t.String(), body); err != nil {
		return ResolvedModelList{}, err
	}
	r.noteUnpinned(t, fetched)
	return ResolvedModelList{Target: t, Models: models, Digest: digest, Fetched: fetched}, nil
}

// ResolveModelTarget resolves model as a remote model list, using newResolver
// when provided or a default Resolver over confDir and the cache directory.
func ResolveModelTarget(ctx context.Context, confDir string, newResolver func(string, Prompter) (Resolver, error), model string) ([]string, error) {
	var (
		res Resolver
		err error
	)
	if newResolver != nil {
		res, err = newResolver(confDir, nil)
	} else {
		cacheDir, cerr := utils.GetClaiCacheDir()
		if cerr != nil {
			return nil, cerr
		}
		res, err = NewResolver(EnvFrom(os.Getenv), confDir, cacheDir, NewClient(), nil, nil)
	}
	if err != nil {
		return nil, fmt.Errorf("model %s: %w", model, err)
	}
	got, err := res.ResolveModelList(ctx, model)
	if err != nil {
		return nil, fmt.Errorf("model %s: %w", model, err)
	}
	return got.Models, nil
}
