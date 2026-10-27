package profiles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const reviewSkill = "---\nname: review\ndescription: Review the staged diff.\n---\nRead the diff."

func writeSkillDir(t *testing.T, root, name, body string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPublishSkill_byNameDirOrFile(t *testing.T) {
	hub := &fakeHub{resp: Published{Address: "cchub:ada/review", Version: 1, Created: true, URL: "https://www.clai.chat/hub/ada/review/", SHA256: "abc"}}
	deps, conf, out := publishDeps(t, hub)
	writeSkillDir(t, filepath.Join(conf, "skills"), "review", reviewSkill)
	elsewhere := writeSkillDir(t, t.TempDir(), "Code Review", "---\ndescription: Review.\n---\nBody")

	if err := deps.PublishSkill(t.Context(), "review", PublishOptions{Note: "first"}); err != nil {
		t.Fatalf("by name: %v", err)
	}
	want := PublishRequest{Kind: "skill", Name: "review", Body: reviewSkill, Note: "first"}
	if hub.got[0] != want {
		t.Fatalf("sent %+v, want %+v", hub.got[0], want)
	}
	if !strings.Contains(out.String(), `"skills": ["cchub:ada/review@1#sha256=abc"]`) {
		t.Fatalf("output %q does not show how to use it", out.String())
	}

	if err := deps.PublishSkill(t.Context(), elsewhere, PublishOptions{}); err == nil || !strings.Contains(err.Error(), "-name") {
		t.Fatalf("a directory that is no hub name: err = %v", err)
	}
	if err := deps.PublishSkill(t.Context(), filepath.Join(elsewhere, "SKILL.md"), PublishOptions{Name: "code-review"}); err != nil {
		t.Fatalf("by file: %v", err)
	}
	if hub.got[1].Name != "code-review" || hub.got[1].Kind != "skill" {
		t.Fatalf("sent %+v", hub.got[1])
	}
}

func TestPublishSkill_refusesBeforeAnythingLeaves(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"nodescription", "---\nname: x\n---\nBody", "missing required description"},
		{"secret", "---\ndescription: d\n---\nuse sk-abc", "credential"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hub := &fakeHub{}
			deps, conf, _ := publishDeps(t, hub)
			writeSkillDir(t, filepath.Join(conf, "skills"), "x", tc.body)
			err := deps.PublishSkill(t.Context(), "x", PublishOptions{})
			if err == nil || !strings.Contains(err.Error(), tc.want) || len(hub.got) != 0 {
				t.Fatalf("err = %v, sent %d; want %q and nothing sent", err, len(hub.got), tc.want)
			}
		})
	}
	hub := &fakeHub{}
	deps, _, _ := publishDeps(t, hub)
	if err := deps.PublishSkill(t.Context(), "missing", PublishOptions{}); err == nil || !strings.Contains(err.Error(), "SKILL.md") {
		t.Fatalf("missing: err = %v", err)
	}
}

func TestSkillsCommand(t *testing.T) {
	hub := &fakeHub{resp: Published{Address: "cchub:ada/review", Version: 1, Created: true}}
	deps, conf, _ := publishDeps(t, hub)
	writeSkillDir(t, filepath.Join(conf, "skills"), "review", reviewSkill)
	c := SkillsCommand(deps)
	if !strings.Contains(c.Help(), "skills publish") || !strings.Contains(c.Help(), `"skills"`) {
		t.Fatalf("help = %q", c.Help())
	}
	sub := c.Subs["publish"]
	if err := sub.Flagset().Parse([]string{"review", "-summary", "Reviews diffs."}); err != nil {
		t.Fatal(err)
	}
	if err := sub.Setup(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := sub.Run(t.Context()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hub.got) != 1 || hub.got[0].Summary != "Reviews diffs." || hub.got[0].Kind != "skill" {
		t.Fatalf("sent %+v", hub.got)
	}
	if err := c.Flagset().Parse([]string{"bogus"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Setup(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := c.Run(t.Context()); err == nil {
		t.Fatal("unknown subcommand: want an error")
	}
}
