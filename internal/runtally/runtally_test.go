package runtally

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeEnv map[string]string

func (e fakeEnv) Getenv(key string) string { return e[key] }

func (e fakeEnv) Setenv(key, value string) error {
	e[key] = value
	return nil
}

func (e fakeEnv) Unsetenv(key string) error {
	delete(e, key)
	return nil
}

func openScope(t *testing.T, env fakeEnv) *Scope {
	t.Helper()
	s, err := Open(env, t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func TestWanted(t *testing.T) {
	for _, tc := range []struct {
		desc    string
		env     fakeEnv
		summary bool
		want    bool
	}{
		{"no summary, no parent", fakeEnv{}, false, false},
		{"summary", fakeEnv{}, true, true},
		{"a parent to report to", fakeEnv{EnvDir: "/somewhere"}, false, true},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			if got := Wanted(tc.env, tc.summary); got != tc.want {
				t.Fatalf("Wanted = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestOpen_pointsChildrenAtItsOwnDirAndCloseRestores(t *testing.T) {
	env := fakeEnv{EnvDir: "/parent"}
	s := openScope(t, env)
	child := env[EnvDir]
	if child == "/parent" || child == "" {
		t.Fatalf("%s = %q, want a fresh directory for the children", EnvDir, child)
	}
	if info, err := os.Stat(child); err != nil || !info.IsDir() {
		t.Fatalf("child dir %q: %v", child, err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if env[EnvDir] != "/parent" {
		t.Fatalf("%s = %q after Close, want /parent back", EnvDir, env[EnvDir])
	}
	if _, err := os.Stat(child); !os.IsNotExist(err) {
		t.Fatalf("child dir still there after Close: %v", err)
	}
}

func TestClose_unsetsWhenThereWasNoParent(t *testing.T) {
	env := fakeEnv{}
	s := openScope(t, env)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, ok := env[EnvDir]; ok {
		t.Fatalf("%s still set after Close", EnvDir)
	}
}

func TestOpen_failsOnAMissingTempRoot(t *testing.T) {
	_, err := Open(fakeEnv{}, filepath.Join(t.TempDir(), "missing"))
	if err == nil {
		t.Fatal("Open succeeded without a temp root")
	}
}

// A child reports into the directory its parent's scope set, and the parent
// collects every report, its children's children included.
func TestReportAndCollect(t *testing.T) {
	env := fakeEnv{}
	lead := openScope(t, env)
	t.Cleanup(func() { _ = lead.Close() })

	worker := openScope(t, env)
	grandchild := openScope(t, env)
	if outcome, err := grandchild.Report(Totals{Runs: 1, PromptTokens: 5, CompletionTokens: 1, UnpricedRuns: 1}); err != nil || outcome != Reported {
		t.Fatalf("grandchild Report = %v, %v", outcome, err)
	}
	if err := grandchild.Close(); err != nil {
		t.Fatalf("grandchild Close: %v", err)
	}
	below, err := worker.Collect()
	if err != nil {
		t.Fatalf("worker Collect: %v", err)
	}
	own := Totals{Runs: 1, PromptTokens: 100, CachedTokens: 40, CompletionTokens: 10, CostUSD: 0.25}
	if outcome, err := worker.Report(own.Add(below)); err != nil || outcome != Reported {
		t.Fatalf("worker Report = %v, %v", outcome, err)
	}
	if err := worker.Close(); err != nil {
		t.Fatalf("worker Close: %v", err)
	}

	got, err := lead.Collect()
	if err != nil {
		t.Fatalf("lead Collect: %v", err)
	}
	want := Totals{Runs: 2, PromptTokens: 105, CachedTokens: 40, CompletionTokens: 11, CostUSD: 0.25, UnpricedRuns: 1}
	if got != want {
		t.Fatalf("Collect = %+v, want %+v", got, want)
	}
}

func TestCollect_skipsUnfinishedWrites(t *testing.T) {
	env := fakeEnv{}
	s := openScope(t, env)
	t.Cleanup(func() { _ = s.Close() })
	if err := os.WriteFile(filepath.Join(env[EnvDir], "run-1.json.tmp"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := s.Collect()
	if err != nil || got != (Totals{}) {
		t.Fatalf("Collect = %+v, %v; want nothing", got, err)
	}
}

func TestCollect_namesABrokenReport(t *testing.T) {
	env := fakeEnv{}
	s := openScope(t, env)
	t.Cleanup(func() { _ = s.Close() })
	if err := os.WriteFile(filepath.Join(env[EnvDir], "run-1.json"), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := s.Collect()
	if err == nil || !strings.Contains(err.Error(), "run-1.json") {
		t.Fatalf("Collect error = %v, want one naming run-1.json", err)
	}
}

func TestReport_outcomes(t *testing.T) {
	t.Run("no parent", func(t *testing.T) {
		s := openScope(t, fakeEnv{})
		t.Cleanup(func() { _ = s.Close() })
		outcome, err := s.Report(Totals{Runs: 1})
		if err != nil || outcome != NoParent {
			t.Fatalf("Report = %v, %v; want %v", outcome, err, NoParent)
		}
	})
	t.Run("parent gone", func(t *testing.T) {
		s := openScope(t, fakeEnv{EnvDir: filepath.Join(t.TempDir(), "gone")})
		t.Cleanup(func() { _ = s.Close() })
		outcome, err := s.Report(Totals{Runs: 1})
		if err != nil || outcome != ParentGone {
			t.Fatalf("Report = %v, %v; want %v", outcome, err, ParentGone)
		}
	})
	t.Run("parent is a file", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(file, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		s := openScope(t, fakeEnv{EnvDir: file})
		t.Cleanup(func() { _ = s.Close() })
		if _, err := s.Report(Totals{Runs: 1}); err == nil {
			t.Fatal("Report into a file succeeded")
		}
	})
}

func TestLine(t *testing.T) {
	for _, tc := range []struct {
		desc string
		t    Totals
		want string
	}{
		{"one priced run", Totals{Runs: 1, PromptTokens: 1200, CompletionTokens: 30, CostUSD: 0.012}, "1 run · 1.2k in · 30 out · ~$0.01"},
		{"cached tokens", Totals{Runs: 4, PromptTokens: 210000, CachedTokens: 90000, CompletionTokens: 9000, CostUSD: 0.41}, "4 runs · 210k in (90k cached) · 9k out · ~$0.41"},
		{"some unpriced", Totals{Runs: 3, PromptTokens: 10, CompletionTokens: 2, CostUSD: 0.5, UnpricedRuns: 1}, "3 runs · 10 in · 2 out · ~$0.50 (1 unpriced)"},
		{"none priced", Totals{Runs: 2, PromptTokens: 10, CompletionTokens: 2, UnpricedRuns: 2}, "2 runs · 10 in · 2 out · cost unknown"},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			if got := tc.t.Line(); got != tc.want {
				t.Fatalf("Line = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestOSEnv(t *testing.T) {
	t.Setenv(EnvDir, "")
	var env OSEnv
	if err := env.Setenv(EnvDir, "x"); err != nil || env.Getenv(EnvDir) != "x" {
		t.Fatalf("Setenv/Getenv: %v", err)
	}
	if err := env.Unsetenv(EnvDir); err != nil || env.Getenv(EnvDir) != "" {
		t.Fatalf("Unsetenv: %v", err)
	}
}

func TestReportOutcome_String(t *testing.T) {
	for o, want := range map[ReportOutcome]string{Reported: "reported", NoParent: "no parent", ParentGone: "parent gone", ReportOutcome(9): "unknown"} {
		if o.String() != want {
			t.Fatalf("%d.String() = %q, want %q", o, o.String(), want)
		}
	}
}
