package profile_test

import (
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/profile"
	"github.com/baalimago/clai/internal/remote"
	"github.com/baalimago/clai/internal/text"
)

// routes are the three ways a body arrives. TestMain lives in schema_test.go:
// testenv.Main already keeps this binary hermetic for both files.
var routes = []profile.Route{profile.File, profile.Remote, profile.Publish}

func routeName(route profile.Route) string {
	switch route {
	case profile.File:
		return "file"
	case profile.Remote:
		return "remote"
	case profile.Publish:
		return "publish"
	}
	return fmt.Sprintf("route(%d)", route)
}

// machine is the Env a test hands the checker: every answer is fixed, so no
// test depends on the machine it runs on.
func machine() profile.Env {
	return profile.Env{
		ResolveModel: func(model string) ([]string, error) {
			switch model {
			case "strong":
				return []string{"good", "fast"}, nil
			case "nonsense", "bogus":
				return nil, nil
			}
			return []string{model}, nil
		},
		HasShellContext: func(name string) bool { return name == "work" },
		ToolNames:       []string{"async_cmd", "cat", "cmd", "file_tree", "rg"},
	}
}

func checked(t *testing.T, body string, env profile.Env) profile.Findings {
	t.Helper()
	return checkedOn(t, body, profile.File, env)
}

func checkedOn(t *testing.T, body string, route profile.Route, env profile.Env) profile.Findings {
	t.Helper()
	findings, err := profile.Validate([]byte(body), route, env)
	if err != nil {
		t.Fatalf("Validate(%s) on %s: %v", body, routeName(route), err)
	}
	return findings
}

func describe(findings profile.Findings) string {
	if len(findings) == 0 {
		return " no findings"
	}
	var b strings.Builder
	for _, f := range findings {
		fmt.Fprintf(&b, "\n  severity=%d member=%s reason=%s routes=%v fixed=%q", f.Severity, f.Member, f.Reason, f.Routes, f.Fixed)
	}
	return b.String()
}

// requireRefused fails the test unless every route refuses findings.
func requireRefused(t *testing.T, findings profile.Findings, what string) {
	t.Helper()
	for _, route := range routes {
		if !findings.Failed(route) {
			t.Errorf("%s does not refuse %s", routeName(route), what)
		}
	}
}

func sortFindings(f, g profile.Finding) int {
	if order := strings.Compare(f.Member, g.Member); order != 0 {
		return order
	}
	if order := strings.Compare(f.Reason, g.Reason); order != 0 {
		return order
	}
	return strings.Compare(f.Fixed, g.Fixed)
}

// documented is the profile member names the schema document defines.
func documented(t *testing.T) []string {
	t.Helper()
	var doc struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(profile.Schema(), &doc); err != nil {
		t.Fatalf("embedded document is not JSON: %v", err)
	}
	names := make([]string, 0, len(doc.Properties))
	for name := range doc.Properties {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// TestValidateValidBody proves clause 12 and acceptance criterion 1: a body the
// document admits is refused on no route.
func TestValidateValidBody(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "examples", "profiles", "*.json"))
	if err != nil {
		t.Fatalf("glob examples: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("examples/profiles holds no profile: the test proves nothing")
	}
	env := profile.Env{
		ResolveModel:    func(model string) ([]string, error) { return []string{model}, nil },
		HasShellContext: func(string) bool { return true },
	}
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var lenient text.Profile
		if err := json.Unmarshal(body, &lenient); err != nil {
			t.Fatalf("%s is no profile the loader reads: %v", path, err)
		}
		for _, route := range routes {
			findings := checkedOn(t, string(body), route, env)
			if findings.Failed(route) {
				t.Errorf("%s is refused on %s:%s", path, routeName(route), describe(findings))
			}
			if problems := findings.Problems(route); len(problems) > 0 {
				t.Errorf("%s on %s: Problems = %v, want none", path, routeName(route), problems)
			}
		}
	}
}

// TestValidateNearMissKey proves clause 2 and acceptance criterion 2.
func TestValidateNearMissKey(t *testing.T) {
	findings := checked(t, `{"promt":"x"}`, machine())
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1:%s", len(findings), describe(findings))
	}
	got := findings[0]
	if got.Severity != profile.Error || got.Member != "promt" || got.Fixed != "prompt" {
		t.Fatalf("finding = severity %d member %s fixed %q, want an error at promt fixed prompt",
			got.Severity, got.Member, got.Fixed)
	}
	if len(got.Routes) != 0 {
		t.Fatalf("routes = %v, want all three", got.Routes)
	}
	for _, route := range routes {
		if !findings.Failed(route) {
			t.Errorf("%s does not refuse a near miss", routeName(route))
		}
	}
	for _, want := range []string{"promt", "prompt"} {
		if !strings.Contains(got.Reason, want) {
			t.Errorf("reason %q does not name %q", got.Reason, want)
		}
	}
	if !slices.Contains(documented(t), got.Fixed) {
		t.Fatalf("Fixed = %q, which the document does not define", got.Fixed)
	}
	t.Run("short key", func(t *testing.T) {
		findings := checked(t, `{"mcp_servers":{"s":{"command":"npx","ur":"https://example.com"}}}`, machine())
		if len(findings) != 1 {
			t.Fatalf("findings = %d, want 1:%s", len(findings), describe(findings))
		}
		got := findings[0]
		if got.Severity != profile.Error || got.Member != "mcp_servers.s.ur" || got.Fixed != "url" {
			t.Fatalf("finding = severity %d member %s fixed %q, want an error at mcp_servers.s.ur fixed url",
				got.Severity, got.Member, got.Fixed)
		}
	})
}

// TestValidateUnknownMemberWarns proves clause 3 and acceptance criterion 3: a
// member the document does not define and that no member is near is a warning.
func TestValidateUnknownMemberWarns(t *testing.T) {
	findings := checked(t, `{"note":"x"}`, machine())
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1:%s", len(findings), describe(findings))
	}
	got := findings[0]
	if got.Severity != profile.Warning || got.Member != "note" || got.Fixed != "" {
		t.Fatalf("finding = severity %d member %s fixed %q, want one warning at note with no fix",
			got.Severity, got.Member, got.Fixed)
	}
	for _, route := range routes {
		if findings.Failed(route) {
			t.Errorf("%s refuses an unknown member that is no near miss", routeName(route))
		}
		if problems := findings.Problems(route); len(problems) != 0 {
			t.Errorf("%s: Problems = %v, want none", routeName(route), problems)
		}
	}
}

// TestValidateDuplicateMember proves clause 4 and acceptance criterion 4: a
// duplicate member name is refused on every route, as one finding at $.
func TestValidateDuplicateMember(t *testing.T) {
	for _, route := range routes {
		findings := checkedOn(t, `{"model":"a","model":"b"}`, route, machine())
		if len(findings) != 1 {
			t.Fatalf("%s: findings = %d, want 1:%s", routeName(route), len(findings), describe(findings))
		}
		got := findings[0]
		if got.Severity != profile.Error || got.Member != "$" {
			t.Fatalf("%s: finding = severity %d member %s, want an error at $",
				routeName(route), got.Severity, got.Member)
		}
		if !strings.Contains(got.Reason, "duplicate") {
			t.Errorf("%s: reason %q does not say what is wrong", routeName(route), got.Reason)
		}
		if !findings.Failed(route) {
			t.Errorf("%s does not refuse a duplicate member name", routeName(route))
		}
	}
	t.Run("nested", func(t *testing.T) {
		findings := checked(t, `{"mcp_servers":{"s":{"command":"npx","command":"curl"}}}`, machine())
		if len(findings) != 1 {
			t.Fatalf("findings = %d, want 1:%s", len(findings), describe(findings))
		}
		got := findings[0]
		if got.Severity != profile.Error || got.Member != "$" || !strings.Contains(got.Reason, "duplicate") {
			t.Fatalf("finding = severity %d member %s reason %q, want an error at $ naming the duplicate",
				got.Severity, got.Member, got.Reason)
		}
		for _, route := range routes {
			if !findings.Failed(route) {
				t.Errorf("%s does not refuse a nested duplicate member name", routeName(route))
			}
		}
	})
}

// TestValidateStrictNames proves clause 4: a name that differs from a
// documented one only by case, '-' or '_' is refused with the name it meant,
// and a body that is not valid UTF-8 is refused at $. The lenient loader binds
// "Model" to model in silence, which is why the checker may not.
func TestValidateStrictNames(t *testing.T) {
	var lenient text.Profile
	if err := json.Unmarshal([]byte(`{"Model":"x"}`), &lenient); err != nil || lenient.Model != "x" {
		t.Fatalf("the lenient loader no longer binds Model in silence (%v, %q)", err, lenient.Model)
	}
	cases := []struct{ name, body, member, fixed string }{
		{"case", `{"Model":"x"}`, "Model", "model"},
		{"underscore", `{"cmd_ban":[]}`, "cmd_ban", "cmd-ban"},
		{"upper", `{"MCP_SERVERS":{}}`, "MCP_SERVERS", "mcp_servers"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			findings := checked(t, c.body, machine())
			if len(findings) != 1 {
				t.Fatalf("findings = %d, want 1:%s", len(findings), describe(findings))
			}
			got := findings[0]
			if got.Severity != profile.Error || got.Member != c.member || got.Fixed != c.fixed {
				t.Fatalf("finding = severity %d member %s fixed %q, want an error at %s fixed %s",
					got.Severity, got.Member, got.Fixed, c.member, c.fixed)
			}
			if len(got.Routes) != 0 {
				t.Fatalf("routes = %v, want all three", got.Routes)
			}
			requireRefused(t, findings, "a case or separator variant")
		})
	}
	t.Run("invalid utf-8", func(t *testing.T) {
		findings := checked(t, "{\"name\":\"\xff\"}", machine())
		if len(findings) != 1 {
			t.Fatalf("findings = %d, want 1:%s", len(findings), describe(findings))
		}
		got := findings[0]
		if got.Severity != profile.Error || got.Member != "$" || !strings.Contains(got.Reason, "UTF-8") {
			t.Fatalf("finding = severity %d member %s reason %q, want an error at $ naming UTF-8",
				got.Severity, got.Member, got.Reason)
		}
	})
}

// TestValidateModelRoutes proves clause 5: model routes through Env.ResolveModel
// and an alias expands to its candidates, each of which must route.
func TestValidateModelRoutes(t *testing.T) {
	t.Run("routes nowhere", func(t *testing.T) {
		findings := checked(t, `{"model":"nonsense"}`, machine())
		if len(findings) != 1 {
			t.Fatalf("findings = %d, want 1:%s", len(findings), describe(findings))
		}
		if got := findings[0]; got.Severity != profile.Error || got.Member != "model" || !strings.Contains(got.Reason, "nonsense") {
			t.Fatalf("finding = severity %d member %s reason %q, want an error at model naming nonsense",
				got.Severity, got.Member, got.Reason)
		}
		for _, route := range routes {
			if !findings.Failed(route) {
				t.Errorf("%s does not refuse a model that routes nowhere", routeName(route))
			}
		}
	})
	t.Run("alias resolves", func(t *testing.T) {
		if findings := checked(t, `{"model":"strong"}`, machine()); len(findings) != 0 {
			t.Fatalf("findings = %d, want 0:%s", len(findings), describe(findings))
		}
	})
	t.Run("alias candidate routes nowhere", func(t *testing.T) {
		var calls []string
		env := profile.Env{ResolveModel: func(model string) ([]string, error) {
			calls = append(calls, model)
			switch model {
			case "strong":
				return []string{"good", "bogus"}, nil
			case "bogus":
				return nil, nil
			}
			return []string{model}, nil
		}}
		findings := checked(t, `{"model":"strong"}`, env)
		if len(findings) != 1 {
			t.Fatalf("findings = %d, want 1:%s", len(findings), describe(findings))
		}
		if got := findings[0]; got.Severity != profile.Error || !strings.Contains(got.Reason, "bogus") {
			t.Fatalf("finding = severity %d reason %q, want an error naming the candidate", got.Severity, got.Reason)
		}
		if want := []string{"strong", "good", "bogus"}; !slices.Equal(calls, want) {
			t.Fatalf("ResolveModel calls = %v, want %v: an alias must not expand recursively", calls, want)
		}
	})
	t.Run("resolver fails", func(t *testing.T) {
		env := profile.Env{ResolveModel: func(string) ([]string, error) {
			return nil, fmt.Errorf("no alias table")
		}}
		findings := checked(t, `{"model":"anything"}`, env)
		if len(findings) != 1 || !strings.Contains(findings[0].Reason, "anything") {
			t.Fatalf("findings =%s, want one error naming the model", describe(findings))
		}
	})
	t.Run("no resolver", func(t *testing.T) {
		if findings := checked(t, `{"model":"anything"}`, profile.Env{}); len(findings) != 0 {
			t.Fatalf("findings = %d, want 0: a nil ResolveModel skips model routing:%s", len(findings), describe(findings))
		}
	})
}

// TestValidateUnknownTool proves clause 6 and acceptance criterion 5: an entry
// that resolves to no tool name is refused on file and remote only, and the
// reason names the tool that was meant.
func TestValidateUnknownTool(t *testing.T) {
	findings := checked(t, `{"use_tools":true,"tools":["filetree"]}`, machine())
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1:%s", len(findings), describe(findings))
	}
	got := findings[0]
	if got.Severity != profile.Error || got.Member != "tools" {
		t.Fatalf("finding = severity %d member %s, want an error at tools", got.Severity, got.Member)
	}
	if want := []profile.Route{profile.File, profile.Remote}; !slices.Equal(got.Routes, want) {
		t.Fatalf("routes = %v, want %v", got.Routes, want)
	}
	for _, want := range []string{"filetree", "file_tree"} {
		if !strings.Contains(got.Reason, want) {
			t.Errorf("reason %q does not name %q", got.Reason, want)
		}
	}
	if !findings.Failed(profile.File) || !findings.Failed(profile.Remote) {
		t.Fatal("file and remote do not refuse an unknown tool")
	}
	if findings.Failed(profile.Publish) {
		t.Fatal("publish has no registry and must not refuse an unknown tool")
	}
	if problems := findings.Problems(profile.Publish); len(problems) != 0 {
		t.Fatalf("publish: Problems = %v, want none", problems)
	}
}

// TestValidateMCPToolEntries proves the namespace exemption: an entry that
// names an MCP tool is not judged, because its name exists only while a
// server runs (architecture/profiles.md, the profile checker). An entry
// outside that namespace is still judged, so the typo catch of clause 6
// stands.
func TestValidateMCPToolEntries(t *testing.T) {
	reachable := []string{"mcp_slivingdoc*", "mcp_*", "mcp_slivingdoc", "mcp_a_b"}
	for _, entry := range reachable {
		t.Run("reachable "+entry, func(t *testing.T) {
			if findings := checked(t, `{"use_tools":true,"tools":["cat","`+entry+`"]}`, machine()); len(findings) != 0 {
				t.Fatalf("findings = %d, want 0: an MCP name is not judged here:%s", len(findings), describe(findings))
			}
		})
	}
	// The exemption is the prefix and nothing else: a glob that is not in the
	// namespace, and a name that only looks like one, stay errors.
	judged := []string{"*notes*", "*mcp_*", "MCP_slivingdoc*", "mcp"}
	for _, entry := range judged {
		t.Run("judged "+entry, func(t *testing.T) {
			findings := checked(t, `{"use_tools":true,"tools":["`+entry+`"]}`, machine())
			if len(findings) != 1 {
				t.Fatalf("findings = %d, want 1:%s", len(findings), describe(findings))
			}
			if findings[0].Severity != profile.Error || findings[0].Member != "tools" {
				t.Fatalf("finding = severity %d member %s, want an error at tools", findings[0].Severity, findings[0].Member)
			}
		})
	}
}

// TestValidateToolWarnings proves clause 6: use_tools false with a tools list,
// and a cmd-ban entry with neither command tool listed, are warnings only; a
// glob, "*" and an empty ToolNames registry are not findings.
func TestValidateToolWarnings(t *testing.T) {
	cases := []struct{ name, body, member string }{
		{"tools unused", `{"use_tools":false,"tools":["rg"]}`, "tools"},
		{"cmd-ban without a command tool", `{"use_tools":true,"tools":["rg"],"cmd-ban":["rm"]}`, "cmd-ban"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			findings := checked(t, c.body, machine())
			if len(findings) != 1 {
				t.Fatalf("findings = %d, want 1:%s", len(findings), describe(findings))
			}
			got := findings[0]
			if got.Severity != profile.Warning || got.Member != c.member {
				t.Fatalf("finding = severity %d member %s, want a warning at %s", got.Severity, got.Member, c.member)
			}
			for _, route := range routes {
				if findings.Failed(route) {
					t.Errorf("%s refuses on a warning", routeName(route))
				}
			}
		})
	}
	t.Run("command tool listed", func(t *testing.T) {
		if findings := checked(t, `{"use_tools":true,"tools":["cmd"],"cmd-ban":["rm"]}`, machine()); len(findings) != 0 {
			t.Fatalf("findings = %d, want 0:%s", len(findings), describe(findings))
		}
	})
	t.Run("glob and star resolve", func(t *testing.T) {
		if findings := checked(t, `{"use_tools":true,"tools":["file*","*"]}`, machine()); len(findings) != 0 {
			t.Fatalf("findings = %d, want 0:%s", len(findings), describe(findings))
		}
	})
	t.Run("no registry", func(t *testing.T) {
		env := machine()
		env.ToolNames = nil
		if findings := checked(t, `{"use_tools":true,"tools":["nothing_like_it"]}`, env); len(findings) != 0 {
			t.Fatalf("findings = %d, want 0: an empty registry checks no name:%s", len(findings), describe(findings))
		}
	})
}

// TestValidateAudioAlias proves clause 7 and acceptance criterion 6:
// audio.model must route and is never read as an alias.
func TestValidateAudioAlias(t *testing.T) {
	findings := checked(t, `{"audio":{"model":"strong"}}`, machine())
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1:%s", len(findings), describe(findings))
	}
	got := findings[0]
	if got.Severity != profile.Error || got.Member != "audio.model" {
		t.Fatalf("finding = severity %d member %s, want an error at audio.model", got.Severity, got.Member)
	}
	for _, want := range []string{"strong", "concrete"} {
		if !strings.Contains(got.Reason, want) {
			t.Errorf("reason %q does not name %q", got.Reason, want)
		}
	}
	t.Run("concrete model", func(t *testing.T) {
		if findings := checked(t, `{"audio":{"model":"good"}}`, machine()); len(findings) != 0 {
			t.Fatalf("findings = %d, want 0:%s", len(findings), describe(findings))
		}
	})
	t.Run("routes nowhere", func(t *testing.T) {
		findings := checked(t, `{"audio":{"model":"nonsense"}}`, machine())
		if len(findings) != 1 || !strings.Contains(findings[0].Reason, "nonsense") {
			t.Fatalf("findings =%s, want one error naming the model", describe(findings))
		}
	})
}

// TestValidatePublishOnlyPolicy proves clauses 8 and 9: one body, three routes,
// differing only on publish.
func TestValidatePublishOnlyPolicy(t *testing.T) {
	body := `{"skills":["gh:acme/agent"],"shell_context":"work"}`
	for _, route := range routes {
		findings := checkedOn(t, body, route, machine())
		switch route {
		case profile.Publish:
			if !findings.Failed(route) {
				t.Fatalf("publish accepts an unpinned skill target and a shell context:%s", describe(findings))
			}
			problems := strings.Join(findings.Problems(route), "\n")
			for _, want := range []string{"shell_context", "@<version>", "@<ref>", "#sha256=<hex>"} {
				if !strings.Contains(problems, want) {
					t.Errorf("publish problems do not name %q: %v", want, findings.Problems(route))
				}
			}
		default:
			if findings.Failed(route) {
				t.Errorf("%s refuses what only publish refuses:%s", routeName(route), describe(findings))
			}
		}
	}
	t.Run("missing shell context", func(t *testing.T) {
		env := machine()
		env.HasShellContext = func(string) bool { return false }
		findings := checked(t, `{"shell_context":"gone"}`, env)
		if len(findings) != 2 {
			t.Fatalf("findings = %d, want 2 (publish's rule and the missing definition):%s", len(findings), describe(findings))
		}
		if !findings.Failed(profile.File) || !findings.Failed(profile.Remote) {
			t.Fatalf("file and remote accept a shell context the config dir does not hold:%s", describe(findings))
		}
		for _, finding := range findings {
			if strings.Contains(finding.Reason, "not a shell context") {
				if want := []profile.Route{profile.File, profile.Remote}; !slices.Equal(finding.Routes, want) {
					t.Fatalf("missing definition routes = %v, want %v: publish cannot ask the author's config dir",
						finding.Routes, want)
				}
			}
		}
		if problems := strings.Join(findings.Problems(profile.Publish), "\n"); strings.Contains(problems, "not a shell context") {
			t.Fatalf("publish problems ask the author's config dir: %v", problems)
		}
	})
	t.Run("no shell context callback", func(t *testing.T) {
		env := machine()
		env.HasShellContext = nil
		findings := checked(t, `{"shell_context":"gone"}`, env)
		if len(findings) != 1 || !findings.Failed(profile.Publish) || findings.Failed(profile.File) {
			t.Fatalf("a nil HasShellContext must skip only the local check:%s", describe(findings))
		}
	})
	t.Run("pinned skill", func(t *testing.T) {
		for _, body := range []string{
			`{"skills":["gh:acme/agent@v1"]}`,
			`{"skills":["cchub:clai/subagents@2"]}`,
			`{"skills":["gh:acme/agent#sha256=bf52c25be2989453e5fec1ad7528560f7b2999a9df86572e7ed168ba33947283"]}`,
		} {
			if findings := checked(t, body, machine()); len(findings) != 0 {
				t.Fatalf("%s: findings = %d, want 0:%s", body, len(findings), describe(findings))
			}
		}
	})
	t.Run("skill target does not parse", func(t *testing.T) {
		findings := checked(t, `{"skills":["nonsense"]}`, machine())
		if len(findings) != 1 {
			t.Fatalf("findings = %d, want 1:%s", len(findings), describe(findings))
		}
		got := findings[0]
		if got.Severity != profile.Error || got.Member != "skills" || !strings.HasPrefix(got.Reason, "skills: nonsense: ") {
			t.Fatalf("finding = severity %d member %s reason %q, want an error at skills naming the target",
				got.Severity, got.Member, got.Reason)
		}
		requireRefused(t, findings, "a skill target that does not parse")
	})
}

// TestValidateNullIsAbsent proves the document's reading of null: Go
// unmarshals null into any field without error, so clai reads a null member as
// absent, and a profile clai's own migration wrote ("args": null, "env": null)
// must check like the body it meant.
func TestValidateNullIsAbsent(t *testing.T) {
	for _, body := range []string{
		`{"model":"test","prompt":"p","audio":null,"skills":null,"mcp_servers":null}`,
		`{"mcp_servers":{"s":{"command":"cat","args":null,"env":null}}}`,
		`{"mcp_servers":{"s":{"command":"cat","auth":null}}}`,
		`{"tools":["cat",null]}`,
	} {
		findings := checked(t, body, machine())
		if len(findings) != 0 {
			t.Errorf("%s: findings = %d, want none:%s", body, len(findings), describe(findings))
		}
	}
	withNull := checked(t, `{"model":null,"note":"x"}`, machine())
	without := checked(t, `{"note":"x"}`, machine())
	if len(withNull) != 1 || len(without) != 1 ||
		withNull[0].Member != without[0].Member || withNull[0].Reason != without[0].Reason {
		t.Errorf("a null member changed the findings: with null%s, without%s", describe(withNull), describe(without))
	}
}

// TestValidateNotAnObject proves clause 1: a body that is not one JSON object
// yields one finding at $ and nothing else.
func TestValidateNotAnObject(t *testing.T) {
	cases := []struct{ name, body string }{
		{"array", `[]`},
		{"two objects", `{} {}`},
		{"empty", ``},
		{"string", `"x"`},
		{"null", `null`},
		{"number", `42`},
		{"truncated", `{"model":`},
		{"trailing garbage", `{"model":"a"} x`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, route := range routes {
				findings := checkedOn(t, c.body, route, machine())
				if len(findings) != 1 {
					t.Fatalf("%s: findings = %d, want 1:%s", routeName(route), len(findings), describe(findings))
				}
				got := findings[0]
				if got.Severity != profile.Error || got.Member != "$" || len(got.Routes) != 0 {
					t.Fatalf("%s: finding = severity %d member %s routes %v, want an error at $ for all routes",
						routeName(route), got.Severity, got.Member, got.Routes)
				}
				if !strings.Contains(got.Reason, "not one JSON object") {
					t.Errorf("%s: reason %q does not say the body is not one JSON object", routeName(route), got.Reason)
				}
				if !findings.Failed(route) {
					t.Errorf("%s accepts a body that is not one JSON object", routeName(route))
				}
			}
		})
	}
}

// TestValidateWrongTypes proves clause 1: every member the document types is
// checked at its own path, with no coercion.
func TestValidateWrongTypes(t *testing.T) {
	cases := []struct{ name, body, member, reason string }{
		{"tools", `{"tools":"rg"}`, "tools", "needs an array"},
		{"use_tools", `{"use_tools":"yes"}`, "use_tools", "needs true or false"},
		{"model", `{"model":5}`, "model", "needs a string"},
		{"mcp_servers", `{"mcp_servers":[]}`, "mcp_servers", "needs an object"},
		{"server", `{"mcp_servers":{"s":[]}}`, "mcp_servers.s", "needs an object"},
		{"startup", `{"mcp_servers":{"s":{"command":"x","startup":"fast"}}}`, "mcp_servers.s.startup", "eager"},
		{"timeout", `{"mcp_servers":{"s":{"command":"x","timeout_seconds":"5"}}}`, "mcp_servers.s.timeout_seconds", "needs an integer"},
		{"fraction", `{"mcp_servers":{"s":{"command":"x","timeout_seconds":1.5}}}`, "mcp_servers.s.timeout_seconds", "needs an integer"},
		{"env value", `{"mcp_servers":{"s":{"command":"x","env":{"K":5}}}}`, "mcp_servers.s.env.K", "needs a string"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			findings := checked(t, c.body, machine())
			if len(findings) != 1 {
				t.Fatalf("findings = %d, want 1:%s", len(findings), describe(findings))
			}
			got := findings[0]
			if got.Severity != profile.Error || got.Member != c.member || !strings.Contains(got.Reason, c.reason) {
				t.Fatalf("finding = severity %d member %s reason %q, want an error at %s saying %q",
					got.Severity, got.Member, got.Reason, c.member, c.reason)
			}
			// A refusal names the member it is about, so two shape findings
			// are two different lines on every run route.
			for _, route := range routes {
				problems := findings.Problems(route)
				if len(problems) != 1 || !strings.HasPrefix(problems[0], c.member+": ") {
					t.Errorf("%s: Problems = %v, want one line starting %q", routeName(route), problems, c.member+": ")
				}
			}
		})
	}
}

// TestValidateServerShape proves clause 1: a server that names neither a
// command nor a url is refused at the server, and no env value is echoed.
func TestValidateServerShape(t *testing.T) {
	const secret = "xoxb-a-secret-value"
	cases := []struct{ name, body string }{
		{"neither", `{"mcp_servers":{"slack":{"env":{"SLACK_TOKEN":"` + secret + `"},"timeout_seconds":5}}}`},
		{"both blank", `{"mcp_servers":{"slack":{"command":"","url":" "}}}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			findings := checked(t, c.body, machine())
			if len(findings) != 1 {
				t.Fatalf("findings = %d, want 1:%s", len(findings), describe(findings))
			}
			got := findings[0]
			if got.Severity != profile.Error || got.Member != "mcp_servers.slack" || got.Reason != `needs "command" or "url"` {
				t.Fatalf("finding = severity %d member %s reason %q, want the server shape error",
					got.Severity, got.Member, got.Reason)
			}
			for _, f := range findings {
				if strings.Contains(f.Reason, secret) {
					t.Fatalf("reason %q echoes an env value", f.Reason)
				}
			}
		})
	}
	t.Run("command", func(t *testing.T) {
		if findings := checked(t, `{"mcp_servers":{"s":{"command":"npx"}}}`, machine()); len(findings) != 0 {
			t.Fatalf("findings = %d, want 0:%s", len(findings), describe(findings))
		}
	})
}

// TestValidateAuthBlock proves clause 1: an invalid auth entry is reported by
// field or index, and a secret is never echoed.
func TestValidateAuthBlock(t *testing.T) {
	const (
		secret      = "s3cr3t-value"
		invalidName = "not a name"
		badRequired = "also bad"
	)
	body := `{"mcp_servers":{"s":{"command":"npx",
		"env":{"LEAKED_SECRET":"` + secret + `"},
		"auth":{"token_env":"` + invalidName + `",
		        "required_env":["POLY_KEY","POLY_KEY","LEAKED_SECRET","` + badRequired + `"]}}}}`
	findings := checked(t, body, machine())
	want := []string{
		"mcp_servers.s.auth.required_env[1]",
		"mcp_servers.s.auth.required_env[2]",
		"mcp_servers.s.auth.required_env[3]",
		"mcp_servers.s.auth.token_env",
	}
	var members []string
	for _, f := range findings {
		if f.Severity != profile.Error {
			t.Errorf("%s: severity = %d, want an error", f.Member, f.Severity)
		}
		members = append(members, f.Member)
		for _, echo := range []string{secret, invalidName, badRequired} {
			if strings.Contains(f.Reason, echo) {
				t.Errorf("reason %q echoes %q", f.Reason, echo)
			}
		}
	}
	slices.Sort(members)
	if !slices.Equal(members, want) {
		t.Fatalf("members = %v, want %v:%s", members, want, describe(findings))
	}
}

// TestValidateRelativeEnvFile proves clause 1: a relative envfile is refused on
// every route, and the reason names the three forms that are allowed.
func TestValidateRelativeEnvFile(t *testing.T) {
	findings := checked(t, `{"mcp_servers":{"s":{"command":"npx","envfile":".env"}}}`, machine())
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1:%s", len(findings), describe(findings))
	}
	got := findings[0]
	if got.Severity != profile.Error || got.Member != "mcp_servers.s.envfile" {
		t.Fatalf("finding = severity %d member %s, want an error at mcp_servers.s.envfile", got.Severity, got.Member)
	}
	for _, want := range []string{"absolute", "~/", "$"} {
		if !strings.Contains(got.Reason, want) {
			t.Errorf("reason %q does not name %q", got.Reason, want)
		}
	}
	for _, route := range routes {
		if !findings.Failed(route) {
			t.Errorf("%s accepts a relative envfile", routeName(route))
		}
	}
	for _, envFile := range []string{"", "~", "~/.clai/.env", "/etc/clai.env", "$HOME/.env"} {
		body := `{"mcp_servers":{"s":{"command":"npx","envfile":"` + envFile + `"}}}`
		if findings := checked(t, body, machine()); len(findings) != 0 {
			t.Fatalf("envfile %q: findings = %d, want 0:%s", envFile, len(findings), describe(findings))
		}
	}
}

// TestValidateBodyCap proves clause 10 and acceptance criterion 8: a body over
// remote.MaxBodyBytes is refused at $ before a member is read.
func TestValidateBodyCap(t *testing.T) {
	const prefix, suffix = `{"prompt":"`, `"}`
	bodyOf := func(size int) string {
		return prefix + strings.Repeat("a", size-len(prefix)-len(suffix)) + suffix
	}
	// The over-cap body is not JSON at all: a checker that decoded before it
	// measured would answer "not one JSON object" instead of the cap.
	over := strings.Repeat("[", remote.MaxBodyBytes+1)
	if len(over) != 65537 {
		t.Fatalf("fixture = %d bytes, want 65537", len(over))
	}
	findings := checked(t, over, machine())
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1:%s", len(findings), describe(findings))
	}
	got := findings[0]
	if got.Severity != profile.Error || got.Member != "$" {
		t.Fatalf("finding = severity %d member %s, want an error at $", got.Severity, got.Member)
	}
	for _, want := range []string{"64", "KiB"} {
		if !strings.Contains(got.Reason, want) {
			t.Errorf("reason %q does not name the cap (%q)", got.Reason, want)
		}
	}
	if strings.Contains(got.Reason, "object") {
		t.Fatalf("reason %q reports the body's shape, so the cap was not checked first", got.Reason)
	}
	t.Run("at the cap", func(t *testing.T) {
		if findings := checked(t, bodyOf(remote.MaxBodyBytes), machine()); len(findings) != 0 {
			t.Fatalf("findings = %d, want 0 at exactly the cap:%s", len(findings), describe(findings))
		}
	})
}

// TestValidateDeterministic proves clause 11 and acceptance criterion 9: the
// same bytes and route produce the same findings, sorted by member then reason.
func TestValidateDeterministic(t *testing.T) {
	body := `{"promt":"x","model":"nonsense","tools":["filetree"],
		"skills":["gh:acme/agent"],"shell_context":"work",
		"$schema":"https://clai.chat/schema/profile-0.9.json",
		"mcp_servers":{"s":{"envfile":".env","auth":{"required_env":["A","A"]}}}}`
	first := checked(t, body, machine())
	if len(first) == 0 {
		t.Fatal("the fixture yields no finding: the test proves nothing")
	}
	if !slices.IsSortedFunc(first, sortFindings) {
		t.Fatalf("findings are not sorted by member then reason:%s", describe(first))
	}
	for range 2 {
		again := checked(t, body, machine())
		if len(again) != len(first) {
			t.Fatalf("findings = %d, want %d", len(again), len(first))
		}
		for i := range first {
			if fmt.Sprint(first[i]) != fmt.Sprint(again[i]) {
				t.Fatalf("finding %d differs between runs: %v and %v", i, first[i], again[i])
			}
		}
	}
}

// TestValidateOffline proves clause 11 and I3: a run opens no socket, starts no
// process and writes no file. The Env callbacks are its only external calls, and
// no production file of the package imports a way to open one.
func TestValidateOffline(t *testing.T) {
	var calls []string
	env := profile.Env{
		ResolveModel: func(model string) ([]string, error) {
			calls = append(calls, "model:"+model)
			return []string{model}, nil
		},
		HasShellContext: func(name string) bool {
			calls = append(calls, "shell:"+name)
			return true
		},
	}
	body := `{"model":"a-model","shell_context":"work",
		"mcp_servers":{"remote":{"url":"https://example.com/mcp"},
		               "local":{"command":"curl","args":["https://example.com/mcp"]}}}`
	findings := checked(t, body, env)
	for _, f := range findings {
		if f.Member != "shell_context" {
			t.Fatalf("finding at %s: a url and a command are never reached:%s", f.Member, describe(findings))
		}
	}
	if want := []string{"model:a-model", "shell:work"}; !slices.Equal(calls, want) {
		t.Fatalf("external calls = %v, want only the Env callbacks %v", calls, want)
	}
	production := productionImports(t)
	for _, forbidden := range []string{"os", "net", "net/http", "os/exec", "syscall"} {
		for file, imports := range production {
			if slices.Contains(imports, forbidden) {
				t.Fatalf("%s imports %q: the checker must open no socket, start no process and write no file",
					file, forbidden)
			}
		}
	}
}

// productionImports is every import of every non-test file in this package.
func productionImports(t *testing.T) map[string][]string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	imports := map[string][]string{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, spec := range file.Imports {
			imports[name] = append(imports[name], strings.Trim(spec.Path.Value, `"`))
		}
	}
	if len(imports) == 0 {
		t.Fatal("no production file in the package: the check proves nothing")
	}
	return imports
}

// TestValidateSchemaVersion proves clause 13 and acceptance criterion 11: a body
// naming another profile-schema version is refused on publish, warned elsewhere,
// and the finding names both versions.
func TestValidateSchemaVersion(t *testing.T) {
	findings := checked(t, `{"$schema":"https://clai.chat/schema/profile-0.9.json"}`, machine())
	var refused, warned bool
	for _, f := range findings {
		if f.Member != "$schema" {
			t.Fatalf("finding at %s, want $schema only:%s", f.Member, describe(findings))
		}
		for _, want := range []string{"0.9", profile.SchemaVersion()} {
			if !strings.Contains(f.Reason, want) {
				t.Errorf("reason %q does not name version %q", f.Reason, want)
			}
		}
		switch f.Severity {
		case profile.Error:
			refused = true
			if want := []profile.Route{profile.Publish}; !slices.Equal(f.Routes, want) {
				t.Errorf("error routes = %v, want %v", f.Routes, want)
			}
		case profile.Warning:
			warned = true
			if want := []profile.Route{profile.File, profile.Remote}; !slices.Equal(f.Routes, want) {
				t.Errorf("warning routes = %v, want %v", f.Routes, want)
			}
		}
	}
	if !refused || !warned {
		t.Fatalf("findings =%s, want an error for publish and a warning for file and remote", describe(findings))
	}
	if !findings.Failed(profile.Publish) {
		t.Error("publish accepts $schema naming another version")
	}
	if findings.Failed(profile.File) || findings.Failed(profile.Remote) {
		t.Error("file and remote must only warn about $schema naming another version")
	}
	t.Run("this version", func(t *testing.T) {
		body := `{"$schema":"https://clai.chat/schema/profile-` + profile.SchemaVersion() + `.json"}`
		if findings := checked(t, body, machine()); len(findings) != 0 {
			t.Fatalf("findings = %d, want 0:%s", len(findings), describe(findings))
		}
	})
	t.Run("not a profile schema", func(t *testing.T) {
		if findings := checked(t, `{"$schema":"https://example.com/schemas/other.json"}`, machine()); len(findings) != 0 {
			t.Fatalf("findings = %d, want 0:%s", len(findings), describe(findings))
		}
	})
}
