package text

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/remote"
)

func overridesFor(t *testing.T, profileJSON string) Configurations {
	t.Helper()
	confDir := t.TempDir()
	t.Setenv("CLAI_CONFIG_DIR", confDir)
	dir := filepath.Join(confDir, "profiles")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bot.json"), []byte(profileJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	conf := Default
	conf.UseProfile = "bot"
	if err := conf.ProfileOverrides(); err != nil {
		t.Fatalf("ProfileOverrides: %v", err)
	}
	return conf
}

func TestProfileOverrides_skillsTurnSkillsOnUnlessTheProfileSaysNo(t *testing.T) {
	on := overridesFor(t, `{"model":"m","skills":["cchub:ada/review@2"]}`)
	if !on.UseSkills || !on.ProfileUseSkillsSet || !slices.Equal(on.ProfileSkills, []string{"cchub:ada/review@2"}) {
		t.Fatalf("skills named: UseSkills=%v set=%v skills=%v", on.UseSkills, on.ProfileUseSkillsSet, on.ProfileSkills)
	}
	off := overridesFor(t, `{"model":"m","use_skills":false,"skills":["cchub:ada/review@2"]}`)
	if off.UseSkills {
		t.Fatal("use_skills false: skills must stay off")
	}
	none := overridesFor(t, `{"model":"m"}`)
	if none.ProfileUseSkillsSet || len(none.ProfileSkills) != 0 {
		t.Fatalf("no skills: set=%v skills=%v", none.ProfileUseSkillsSet, none.ProfileSkills)
	}
}

func TestResolveProfileSkills(t *testing.T) {
	ok := func(_ context.Context, targets []string) ([]string, error) {
		return []string{"/cache/" + strings.Join(targets, ",")}, nil
	}
	off := &Configurations{UseSkills: false, ProfileSkills: []string{"cchub:ada/x@1"}}
	if roots, err := resolveProfileSkills(t.Context(), off, nil); err != nil || roots != nil {
		t.Fatalf("skills off: %v, %v; want nothing fetched", roots, err)
	}
	on := &Configurations{UseSkills: true, UseProfile: "bot", ProfileSkills: []string{"cchub:ada/x@1"}}
	if _, err := resolveProfileSkills(t.Context(), on, nil); err == nil {
		t.Fatal("no resolver: want an error")
	}
	if roots, err := resolveProfileSkills(t.Context(), on, ok); err != nil || len(roots) != 1 || roots[0] != "/cache/cchub:ada/x@1" {
		t.Fatalf("resolved: %v, %v", roots, err)
	}
	refused := func(context.Context, []string) ([]string, error) {
		return nil, &remote.UntrustedError{Target: "cchub:ada/x@1"}
	}
	_, err := resolveProfileSkills(t.Context(), on, refused)
	var untrusted *remote.UntrustedError
	if !errors.As(err, &untrusted) || !strings.Contains(err.Error(), "profile bot") {
		t.Fatalf("refused: err = %v, want the resolver's error naming the profile", err)
	}
}

func TestRemoteRun_aProfileTrustedAtThePromptCarriesItsSkills(t *testing.T) {
	run := &remoteRun{
		interactive: true,
		trustInput:  strings.NewReader("y\n"),
		newResolver: func(_ string, p remote.Prompter) (remote.Resolver, error) {
			return remote.Resolver{}, errors.New("stop")
		},
	}
	if _, err := run.skills(t.Context(), []string{"cchub:ada/x@1"}); err == nil || err.Error() != "stop" {
		t.Fatalf("factory error: %v", err)
	}
	var prompt remote.Prompter
	run.newResolver = func(_ string, p remote.Prompter) (remote.Resolver, error) {
		prompt = p
		return remote.Resolver{}, errors.New("stop")
	}
	_, _ = run.profile(t.Context(), "cchub:ada/bot")
	if run.consented {
		t.Fatal("consented before anyone answered")
	}
	if ok, err := prompt(t.Context(), remote.TrustPrompt{Target: "cchub:ada/bot"}); err != nil || !ok {
		t.Fatalf("prompt: %v %v", ok, err)
	}
	if !run.consented {
		t.Fatal("a yes at the profile's prompt must carry its skills")
	}
}
