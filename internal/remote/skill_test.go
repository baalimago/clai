package remote

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const reviewSkill = "---\nname: review\ndescription: Review the staged diff.\n---\nRead the diff, then comment."

func TestParseSkillTarget(t *testing.T) {
	for _, tc := range []struct{ in, want, fetch string }{
		{"cchub:ada/review@2", "cchub:ada/review@2", "/hub/ada/review/v/2/raw"},
		{"gh:anthropics/skills/skills/pdf@abc", "gh:anthropics/skills/skills/pdf/SKILL.md@abc", "https://raw.githubusercontent.com/anthropics/skills/abc/skills/pdf/SKILL.md"},
		{"gh:ada/review", "gh:ada/review/SKILL.md", "https://raw.githubusercontent.com/ada/review/HEAD/SKILL.md"},
		{"gh:ada/kit/review/SKILL.md@v1", "gh:ada/kit/review/SKILL.md@v1", "https://raw.githubusercontent.com/ada/kit/v1/review/SKILL.md"},
		{"https://example.com/review/SKILL.md", "https://example.com/review/SKILL.md", "https://example.com/review/SKILL.md"},
	} {
		got, err := ParseSkillTarget(tc.in)
		if err != nil {
			t.Fatalf("ParseSkillTarget(%q): %v", tc.in, err)
		}
		if got.String() != tc.want || !strings.HasSuffix(got.FetchURL("https://hub"), tc.fetch) {
			t.Fatalf("ParseSkillTarget(%q) = %s fetching %s", tc.in, got, got.FetchURL("https://hub"))
		}
	}
	if _, err := ParseSkillTarget("review"); !errors.Is(err, ErrNotRemote) {
		t.Fatalf("a bare name: err = %v, want ErrNotRemote", err)
	}
}

func TestResolveSkill_fetchesChecksTrustsAndLaysOutASkillDir(t *testing.T) {
	h := newHub(t, map[string]string{"/hub/ada/code-review/v/2/raw": reviewSkill})
	h.ctype = "text/markdown; charset=utf-8"
	var asked []TrustPrompt
	r := testResolver(t, h, func(_ context.Context, p TrustPrompt) (bool, error) {
		asked = append(asked, p)
		return true, nil
	})
	got, err := r.ResolveSkill(t.Context(), "cchub:ada/code-review@2", ConsentAsk)
	if err != nil {
		t.Fatalf("ResolveSkill: %v", err)
	}
	if got.Name != "review" || got.Description != "Review the staged diff." || !got.Fetched {
		t.Fatalf("ResolvedSkill = %+v", got)
	}
	b, err := os.ReadFile(filepath.Join(got.Root, "review", "SKILL.md"))
	if err != nil || string(b) != reviewSkill {
		t.Fatalf("SKILL.md = %q, %v", b, err)
	}
	if len(asked) != 1 || !strings.Contains(asked[0].Summary.String(), "Review the staged diff.") {
		t.Fatalf("asked = %+v", asked)
	}

	r.Prompt = nil
	again, err := r.ResolveSkill(t.Context(), "cchub:ada/code-review@2", ConsentAsk)
	if err != nil || again.Fetched || again.Root != got.Root {
		t.Fatalf("pinned and trusted: %+v, %v; want it from the cache", again, err)
	}
}

func TestResolveSkill_namesTheSkillFromItsAddressWithoutAFrontmatterName(t *testing.T) {
	h := newHub(t, map[string]string{"/hub/ada/triage/v/1/raw": "---\ndescription: Triage.\n---\nBody"})
	r := testResolver(t, h, nil)
	got, err := r.ResolveSkill(t.Context(), "cchub:ada/triage@1", ConsentGiven)
	if err != nil || got.Name != "triage" {
		t.Fatalf("ResolveSkill = %+v, %v", got, err)
	}
}

func TestResolveSkill_refusals(t *testing.T) {
	h := newHub(t, map[string]string{
		"/hub/ada/plain/v/1/raw":  "# no frontmatter",
		"/hub/ada/review/v/1/raw": reviewSkill,
	})
	r := testResolver(t, h, nil)
	var invalid *InvalidSkillError
	if _, err := r.ResolveSkill(t.Context(), "cchub:ada/plain@1", ConsentGiven); !errors.As(err, &invalid) || invalid.ExitCode() != ExitRefused {
		t.Fatalf("no description: err = %v, want InvalidSkillError", err)
	}
	var untrusted *UntrustedError
	if _, err := r.ResolveSkill(t.Context(), "cchub:ada/review@1", ConsentAsk); !errors.As(err, &untrusted) {
		t.Fatalf("no terminal: err = %v, want UntrustedError", err)
	}
	var mismatch *HashMismatchError
	if _, err := r.ResolveSkill(t.Context(), "cchub:ada/review@1#sha256="+strings.Repeat("0", 64), ConsentAsk); !errors.As(err, &mismatch) {
		t.Fatalf("wrong hash: err = %v", err)
	}
	if _, err := r.ResolveSkill(t.Context(), "cchub:ada/missing@1", ConsentGiven); err == nil {
		t.Fatal("missing: want an error")
	}
}

func TestProfileSkills(t *testing.T) {
	got, err := ProfileSkills([]byte(`{"skills":["cchub:ada/review@2","gh:ada/kit/triage"]}`))
	if err != nil || len(got) != 2 || got[1].String() != "gh:ada/kit/triage/SKILL.md" {
		t.Fatalf("ProfileSkills = %v, %v", got, err)
	}
	if got, err := ProfileSkills([]byte(`{"model":"m"}`)); err != nil || len(got) != 0 {
		t.Fatalf("no skills: %v, %v", got, err)
	}
	for _, bad := range []string{`{"skills":"cchub:ada/x"}`, `{"skills":["./local"]}`, `{"skills":["cchub:bad"]}`} {
		if _, err := ProfileSkills([]byte(bad)); err == nil {
			t.Fatalf("ProfileSkills(%s): want an error", bad)
		}
	}
}

func TestValidate_checksSkills(t *testing.T) {
	if _, err := Validate("x", []byte(`{"model":"m","skills":["cchub:ada/review@2"]}`)); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	_, err := Validate("x", []byte(`{"model":"m","skills":["review"]}`))
	if err == nil || !strings.Contains(err.Error(), "skills") {
		t.Fatalf("a bare skill name: err = %v", err)
	}
}

func TestSummary_listsSkills(t *testing.T) {
	body := []byte(`{"model":"m","skills":["cchub:ada/review@2","gh:ada/kit/triage"]}`)
	checked, err := Validate("x", body)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Summarize(body, checked)
	if err != nil {
		t.Fatal(err)
	}
	if out := s.String(); !strings.Contains(out, "▸ skills · 2\n  cchub:ada/review@2\n  gh:ada/kit/triage/SKILL.md   ! not pinned\n") {
		t.Fatalf("summary = %q", out)
	}
}
