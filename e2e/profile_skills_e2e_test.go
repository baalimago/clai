package e2e

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/remote"
)

const (
	reviewSkillMD = "---\nname: review\ndescription: Review the staged diff for bugs.\n---\nRead the diff, then list the bugs."
	triageSkillMD = "---\ndescription: Triage new issues by label.\n---\nLabel each issue."
	skilledAgent  = `{"model":"test","prompt":"You are the skilled agent.","skills":["cchub:ada/review@1","gh:ada/kit/triage@v1"]}`
)

// setupSkilledAgent publishes a review skill on the hub, a triage skill
// in a GitHub repo, and ada/agent, which names both.
func setupSkilledAgent(t *testing.T) remoteE2E {
	t.Helper()
	env := setupRemoteE2E(t)
	env.hub.publishSkill("ada/review", reviewSkillMD)
	env.hub.serveFile("/gh/ada/kit/v1/triage/SKILL.md", "text/plain; charset=utf-8", triageSkillMD)
	env.hub.publish("ada/agent", skilledAgent)
	return env
}

// A profile's skills reach the model: installed once, they run from the
// trust store and the cache, offline too, with no further fetch.
func Test_SKILLS_installedProfileBringsItsSkills(t *testing.T) {
	env := setupSkilledAgent(t)
	status, stdout, stderr := runRemote(t, "profiles install cchub:ada/agent@1")
	if status != 0 {
		t.Fatalf("install: status %d stdout %q stderr %q", status, stdout, stderr)
	}
	for _, want := range []string{"skills: cchub:ada/review@1, gh:ada/kit/triage/SKILL.md@v1", "Skill review from cchub:ada/review@1", "Skill triage from gh:ada/kit/triage/SKILL.md@v1"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("install output %q lacks %q", stdout, want)
		}
	}
	for _, want := range []string{"cchub:ada/review", "gh:ada/kit/triage/SKILL.md", digestOf(reviewSkillMD), digestOf(triageSkillMD)} {
		if !strings.Contains(env.trusted(t), want) {
			t.Fatalf("trust.json lacks %q: %s", want, env.trusted(t))
		}
	}

	fetched := env.hub.fetches()
	t.Setenv("CLAI_CLOUD", "off")
	status, stdout, stderr = runRemote(t, "-r -p agent q hello")
	if status != 0 || stdout != "hello\n" {
		t.Fatalf("offline run: status %d stdout %q stderr %q", status, stdout, stderr)
	}
	conv := env.savedConversation(t)
	for _, want := range []string{"You are the skilled agent.", "Review the staged diff for bugs.", "Triage new issues by label."} {
		if !strings.Contains(conv, want) {
			t.Fatalf("the conversation lacks %q: %s", want, conv)
		}
	}
	if env.hub.fetches() != fetched {
		t.Fatalf("an installed profile fetched again: %d requests, want %d", env.hub.fetches(), fetched)
	}
}

// A hash on the profile is consent to the profile's bytes, not to its
// skills': a skill nobody trusted stops a run with no terminal.
func Test_SKILLS_untrustedSkillStopsARunWithoutATerminal(t *testing.T) {
	env := setupSkilledAgent(t)
	status, stdout, stderr := runRemote(t, "-r -prp cchub:ada/agent@1#sha256="+digestOf(skilledAgent)+" q hello")
	if status != remote.ExitRefused || stdout != "" {
		t.Fatalf("status %d stdout %q stderr %q, want 3 and no output", status, stdout, stderr)
	}
	if !strings.Contains(stderr, "skill cchub:ada/review@1") || !strings.Contains(stderr, "clai profiles trust cchub:ada/review@1") {
		t.Fatalf("stderr %q does not name the skill and how to trust it", stderr)
	}

	status, _, stderr = runRemote(t, "profiles trust cchub:ada/agent@1")
	if status != 0 {
		t.Fatalf("trust: status %d stderr %q", status, stderr)
	}
	status, stdout, stderr = runRemote(t, "-r -prp cchub:ada/agent@1 q hello")
	if status != 0 || stdout != "hello\n" || !strings.Contains(env.savedConversation(t), "Review the staged diff for bugs.") {
		t.Fatalf("trusted run: status %d stdout %q stderr %q", status, stdout, stderr)
	}
}

func Test_SKILLS_refusals(t *testing.T) {
	env := setupSkilledAgent(t)
	env.hub.publishSkill("ada/vague", "# a skill with no description")
	env.hub.publish("ada/vague-agent", `{"model":"test","skills":["cchub:ada/vague@1"]}`)
	env.hub.publish("ada/lost-agent", `{"model":"test","skills":["cchub:ada/gone@1"]}`)
	env.hub.publish("ada/bare-agent", `{"model":"test","skills":["review"]}`)
	for _, tc := range []struct{ target, want string }{
		{"cchub:ada/vague-agent@1", "missing required description"},
		{"cchub:ada/lost-agent@1", "cchub:ada/gone@1: not found"},
		{"cchub:ada/bare-agent@1", `"review" is not a skill target`},
	} {
		status, _, stderr := runRemote(t, "profiles install "+tc.target)
		if status == 0 || !strings.Contains(stderr, tc.want) {
			t.Fatalf("install %s: status %d stderr %q, want %q", tc.target, status, stderr, tc.want)
		}
	}
	if _, err := os.Stat(filepath.Join(env.confDir, "profiles", "vague-agent.json")); err == nil {
		t.Fatal("a profile whose skill was refused was installed anyway")
	}
}

// The round trip Lorentz asked for: publish a skill, publish an agent that
// names it, and run the agent by its address on another machine.
func Test_SKILLS_publishASkillThenAnAgentThatUsesIt(t *testing.T) {
	env := setupRemoteE2E(t)
	api := httptest.NewServer(env.hub)
	t.Cleanup(api.Close)
	hubURL := env.hub.srv.URL
	t.Setenv("CLAI_CLOUD_TOKEN", fakeHubToken)
	skillDir := filepath.Join(env.confDir, "skills", "review")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(reviewSkillMD), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("CLAI_CLOUD_URL", api.URL)
	status, stdout, stderr := runRemote(t, "skills publish review -handle ada")
	pinned := "cchub:ada/review@1#sha256=" + digestOf(reviewSkillMD)
	if status != 0 || !strings.Contains(stdout, `"skills": ["`+pinned+`"]`) {
		t.Fatalf("skills publish: status %d stdout %q stderr %q", status, stdout, stderr)
	}

	agent := `{"model":"test","prompt":"You review.","skills":["` + pinned + `"]}`
	if err := os.WriteFile(filepath.Join(env.confDir, "profiles", "reviewer.json"), []byte(agent), 0o644); err != nil {
		t.Fatal(err)
	}
	status, stdout, stderr = runRemote(t, "profiles publish reviewer")
	if status != 0 || !strings.Contains(stdout, "Published cchub:ada/reviewer@1") {
		t.Fatalf("profiles publish: status %d stdout %q stderr %q", status, stdout, stderr)
	}
	sent := e2eReadProfile(t, env, "reviewer")

	t.Setenv("CLAI_CLOUD_URL", hubURL)
	status, _, stderr = runRemote(t, "profiles trust cchub:ada/reviewer@1")
	if status != 0 {
		t.Fatalf("trust: status %d stderr %q", status, stderr)
	}
	status, stdout, stderr = runRemote(t, "-r -prp cchub:ada/reviewer@1#sha256="+digestOf(sent)+" q hi")
	if status != 0 || stdout != "hi\n" || !strings.Contains(env.savedConversation(t), "Review the staged diff for bugs.") {
		t.Fatalf("run: status %d stdout %q stderr %q", status, stdout, stderr)
	}
}

func e2eReadProfile(t *testing.T, env remoteE2E, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(env.confDir, "profiles", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
