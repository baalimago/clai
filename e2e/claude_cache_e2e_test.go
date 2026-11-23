package e2e

import (
	"strings"
	"testing"

	"github.com/baalimago/clai/e2e/e2etest"
)

func setupClaudeE2E(t *testing.T) forkEnv {
	t.Helper()
	confDir := e2etest.SetupMainTestConfigDir(t)
	prov := e2etest.StartClaudeProvider(t, "the reply")
	prov.WriteClaudeModel(t, confDir, "claude-x")
	e2etest.WriteTextConfig(t, confDir, "claude-x")
	return forkEnv{confDir: confDir, prov: prov}
}

func Test_e2e_claude_cacheField(t *testing.T) {
	env := setupClaudeE2E(t)

	stdout, _ := env.mustRun(t, "-r q hello")

	if !strings.Contains(stdout, "the reply") {
		t.Fatalf("stdout = %q, want the answer", stdout)
	}
	req := decodeRequests(t, env.prov, 1)[0]
	if got := string(req.Rest["cache_control"]); got != `{"type":"ephemeral"}` {
		t.Fatalf("top-level cache_control = %q", got)
	}
	if n := strings.Count(req.Body, "cache_control"); n != 1 {
		t.Fatalf("body mentions cache_control %d times, want only the top-level member: %s", n, req.Body)
	}
}

func Test_e2e_claude_forkReadsCache(t *testing.T) {
	env := setupClaudeE2E(t)
	env.seed(t, "-r q hello")

	_, stderr := env.mustRun(t, "-r -qs chat fork 0 task")

	reqs := decodeRequests(t, env.prov, 2)
	for k, v := range reqs[0].Rest {
		if string(v) != string(reqs[1].Rest[k]) {
			t.Fatalf("member %q changed: %s then %s", k, v, reqs[1].Rest[k])
		}
	}
	if len(reqs[0].Rest) != len(reqs[1].Rest) || reqs[1].Rest["cache_control"] == nil {
		t.Fatalf("members differ or lack cache_control: %v then %v", reqs[0].Rest, reqs[1].Rest)
	}
	if got := env.prov.Usage(1).Cached; got == 0 || got != env.prov.Usage(0).Prompt {
		t.Fatalf("cache read = %d, want the first request's prompt %d", got, env.prov.Usage(0).Prompt)
	}
	if !strings.Contains(stderr, "(10 cached)") {
		t.Fatalf("usage line lacks (10 cached): %q", stderr)
	}
}

func Test_e2e_openrouter_claude_isNotCached(t *testing.T) {
	confDir := e2etest.SetupMainTestConfigDir(t)
	prov := e2etest.StartCachingProvider(t, "the reply")
	prov.WriteOpenRouterModel(t, confDir, "anthropic/claude-x")
	e2etest.WriteTextConfig(t, confDir, "or:anthropic/claude-x")
	env := forkEnv{confDir: confDir, prov: prov}

	env.seed(t, "-r q hello")
	env.mustRun(t, "-r chat fork 0 task")

	for i, req := range decodeRequests(t, prov, 2) {
		if strings.Contains(req.Body, "cache_control") {
			t.Fatalf("request %d carries cache_control: %s", i, req.Body)
		}
		if !strings.Contains(req.Model, "claude-x") {
			t.Fatalf("request %d model = %q, want the OpenRouter claude model", i, req.Model)
		}
	}
}
