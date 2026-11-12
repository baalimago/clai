package configschema_test

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/configschema"
)

// validBodies holds one body per kind that its document accepts whole: every
// member the document names, every $ref followed, every array and map filled.
var validBodies = map[configschema.Kind]string{
	configschema.McpServers: `{
		"name": "ddg",
		"command": "uvx",
		"args": ["duckduckgo-mcp"],
		"url": "https://example.test/mcp",
		"env": {"DDG_KEY": "x"},
		"envfile": "~/keys.env",
		"timeout_seconds": 30,
		"startup": "eager",
		"connect_timeout_seconds": 5,
		"auth": {"token_command": ["echo", "token"], "token_env": "DDG_KEY", "scopes": ["search"], "required_env": ["DDG_KEY"]},
		"auth_timeout_seconds": 10,
		"optional": true
	}`,
	configschema.ShellContexts: `{
		"shell": "bash",
		"timeout_ms": 5000,
		"timed_out_value": "TIMEOUT",
		"error_value": "ERROR",
		"template": "{{.cmd}}",
		"vars": {"REPO": "/tmp/repo"}
	}`,
	configschema.TextConfig: `{
		"model": "sonnet",
		"model-aliases": {"fast": ["haiku", "sonnet"]},
		"system-prompt": "be brief",
		"raw": false,
		"use-tools": true,
		"cmd-mode-prompt": "run it",
		"tool-output-rune-limit": 4000,
		"save-reply-as-prompt": true,
		"cmd-ban": ["rm"],
		"summarize-conversations": true,
		"summary-model": "haiku",
		"query-summary": false,
		"use_lookback": true,
		"max-tool-calls": 12,
		"stoploss": {"max-tokens": 1000, "max-tokens-handover-instructions": "hand over", "max-tool-calls-after-handover": 2}
	}`,
	configschema.PhotoConfig: `{
		"model": "dalle",
		"model-aliases": {"art": ["dalle"]},
		"prompt-format": "text",
		"output": {"type": "local", "dir": "~/pics", "prefix": "shot"},
		"raw": false,
		"auto-open-images": true,
		"image-opener": "xdg-open"
	}`,
	configschema.VideoConfig: `{
		"model": "veo",
		"prompt-format": "text",
		"output": {"type": "url", "dir": "~/clips", "prefix": "clip"},
		"raw": true
	}`,
	configschema.Theme: `{
		"primary": "#ffffff",
		"secondary": "#000000",
		"breadtext": "#cccccc",
		"roleSystem": "blue",
		"roleUser": "green",
		"roleTool": "yellow",
		"roleReasoning": "magenta",
		"roleOther": "white",
		"notificationBell": true,
		"tableItems": 12,
		"toolOutputRows": 4,
		"rollingOutput": {"enabled": true, "windowCellHeight": 3},
		"loopTitle": "loop"
	}`,
	configschema.SkillsJSON: `{
		"enabled": true,
		"globalSkillDirs": ["~/skills"],
		"projectSkillDirs": ["./skills"],
		"trust_all_skills": false,
		"maxActivatedSkills": 4
	}`,
}

// Proves clause 1: a body the kind's document accepts returns no finding, for
// every kind, with no network access.
func TestCheckValidBodies(t *testing.T) {
	if got, want := slices.Sorted(maps.Keys(validBodies)), configschema.Kinds(); !slices.Equal(got, want) {
		t.Fatalf("validBodies covers %v, want the kinds %v", got, want)
	}
	for _, kind := range configschema.Kinds() {
		body := validBodies[kind]
		findings, err := configschema.Check(kind, []byte(body))
		if err != nil {
			t.Fatalf("%s: Check(%s) = %v, want no error", kind, body, err)
		}
		if len(findings) != 0 {
			t.Fatalf("%s: Check(%s) = %v, want no finding", kind, body, findings)
		}
	}
}

// Proves clause 1: a repeated member name, a value that is no UTF-8 and an
// integer written as 2.0 or 1e3 are one Error each, at the member they are in;
// a body that is no JSON value is one Error at "$" and no other finding.
func TestCheckSyntax(t *testing.T) {
	rows := []struct {
		name string
		kind configschema.Kind
		body string
		want configschema.Finding
	}{
		{"duplicate member", configschema.Theme, `{"primary": "#fff", "primary": "#000"}`, configschema.Finding{
			Member:   "primary",
			Reason:   "appears more than once: a JSON object holds one value per member",
			Severity: configschema.Error,
		}},
		{"invalid UTF-8", configschema.Theme, "{\"primary\": \"\xff\"}", configschema.Finding{
			Member:   "primary",
			Reason:   "is not valid UTF-8",
			Severity: configschema.Error,
		}},
		{"2.0 for an integer", configschema.Theme, `{"tableItems": 2.0}`, configschema.Finding{
			Member:   "tableItems",
			Reason:   "needs an integer",
			Severity: configschema.Error,
		}},
		{"1e3 for an integer", configschema.Theme, `{"tableItems": 1e3}`, configschema.Finding{
			Member:   "tableItems",
			Reason:   "needs an integer",
			Severity: configschema.Error,
		}},
		{"body is no JSON value", configschema.Theme, `{"primary": }`, configschema.Finding{
			Member:   "$",
			Reason:   "not one JSON value",
			Severity: configschema.Error,
		}},
		{"body is empty", configschema.Theme, "  \n", configschema.Finding{
			Member:   "$",
			Reason:   "not one JSON value: the body is empty",
			Severity: configschema.Error,
		}},
		{"body holds two values", configschema.Theme, `{"primary": "#fff"} {}`, configschema.Finding{
			Member:   "$",
			Reason:   "not one JSON value: the body holds more than one JSON value",
			Severity: configschema.Error,
		}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			findings, err := configschema.Check(row.kind, []byte(row.body))
			if err != nil {
				t.Fatalf("Check(%s) = %v, want no error", row.body, err)
			}
			if len(findings) != 1 {
				t.Fatalf("Check(%s) = %v, want exactly one finding", row.body, findings)
			}
			if got := findings[0]; got != row.want {
				t.Fatalf("Check(%s) = %+v, want %+v", row.body, got, row.want)
			}
		})
	}
}

// Proves clauses 2 and 3: a value that is not the document's type, and a name
// that is a near miss of one the document writes, are Errors naming the member
// and, for a near miss, the member it means.
func TestCheckShapes(t *testing.T) {
	rows := []struct {
		name    string
		kind    configschema.Kind
		body    string
		member  string
		reasons []string
	}{
		{"wrong type", configschema.Theme, `{"notificationBell": "yes"}`, "notificationBell", []string{"needs true or false"}},
		{"wrong type under a $ref", configschema.TextConfig, `{"stoploss": {"max-tokens": "many"}}`, "stoploss.max-tokens", []string{"needs an integer"}},
		{"wrong type at an array item", configschema.SkillsJSON, `{"globalSkillDirs": ["~/skills", 2]}`, "globalSkillDirs[1]", []string{"needs a string"}},
		{"wrong type at a map value", configschema.ShellContexts, `{"vars": {"REPO": 7}}`, "vars.REPO", []string{"needs a string"}},
		{"wrong type for the whole body", configschema.Theme, `[]`, "$", []string{"needs an object"}},
		{"word outside the enum", configschema.McpServers, `{"startup": "fast"}`, "startup", []string{"needs one of", `"eager"`, `"lazy"`}},
		{"near-miss name", configschema.Theme, `{"primarry": "#fff"}`, "primarry", []string{`unknown member "primarry"`, `did you mean "primary"?`}},
		{"other separator", configschema.TextConfig, `{"use_tools": true}`, "use_tools", []string{`unknown member "use_tools"`, `did you mean "use-tools"?`}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			findings, err := configschema.Check(row.kind, []byte(row.body))
			if err != nil {
				t.Fatalf("Check(%s) = %v, want no error", row.body, err)
			}
			if len(findings) != 1 {
				t.Fatalf("Check(%s) = %v, want exactly one finding", row.body, findings)
			}
			got := findings[0]
			if got.Member != row.member {
				t.Fatalf("Check(%s) member = %q, want %q", row.body, got.Member, row.member)
			}
			if got.Severity != configschema.Error {
				t.Fatalf("Check(%s) severity = %v, want an Error", row.body, got.Severity)
			}
			for _, reason := range row.reasons {
				if !strings.Contains(got.Reason, reason) {
					t.Fatalf("Check(%s) reason = %q, want it to name %q", row.body, got.Reason, reason)
				}
			}
		})
	}
}

// Proves clause 4: a member the document marks x-clai: unread is one Warning,
// and a member the document does not name and that is no near miss of one it
// does name is no finding at all.
func TestCheckUnread(t *testing.T) {
	body := `{"$schema": "https://clai.chat/schema/theme-1.0.json", "wibble": {"anything": [1, 2]}}`
	findings, err := configschema.Check(configschema.Theme, []byte(body))
	if err != nil {
		t.Fatalf("Check(%s) = %v, want no error", body, err)
	}
	if len(findings) != 1 {
		t.Fatalf("Check(%s) = %v, want one finding: the unread member and no other", body, findings)
	}
	got := findings[0]
	if got.Member != "$schema" {
		t.Fatalf("Check(%s) member = %q, want %q", body, got.Member, "$schema")
	}
	if got.Severity != configschema.Warning {
		t.Fatalf("Check(%s) severity = %v, want a Warning", body, got.Severity)
	}
	if !strings.Contains(got.Reason, "x-clai: unread") {
		t.Fatalf("Check(%s) reason = %q, want it to say why the member is unread", body, got.Reason)
	}
}

// corpus is the shared conformance fixture: one body per case and the findings
// both checkers must answer with, in order. The Worker's evaluator reads the
// same file.
type corpus struct {
	Cases []struct {
		Kind     string `json:"kind"`
		Name     string `json:"name"`
		Body     string `json:"body"`
		Findings []struct {
			Member   string `json:"member"`
			Reason   string `json:"reason"`
			Severity string `json:"severity"`
		} `json:"findings"`
	} `json:"cases"`
}

// Proves clause 6: every corpus case returns exactly its listed findings, in
// order, with no clock read.
func TestConformanceCorpus(t *testing.T) {
	raw, err := os.ReadFile("testdata/conformance.json")
	if err != nil {
		t.Fatalf("read the conformance corpus: %v", err)
	}
	var fixture corpus
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("decode the conformance corpus: %v", err)
	}
	if len(fixture.Cases) == 0 {
		t.Fatal("the conformance corpus holds no case")
	}
	kinds := map[configschema.Kind]bool{}
	for _, row := range fixture.Cases {
		kinds[configschema.Kind(row.Kind)] = true
	}
	for _, kind := range configschema.Kinds() {
		if !kinds[kind] {
			t.Fatalf("the conformance corpus holds no %s case", kind)
		}
	}
	for _, row := range fixture.Cases {
		t.Run(row.Kind+" "+row.Name, func(t *testing.T) {
			findings, err := configschema.Check(configschema.Kind(row.Kind), []byte(row.Body))
			if err != nil {
				t.Fatalf("%s: Check = %v, want no error", row.Name, err)
			}
			want := make([]configschema.Finding, 0, len(row.Findings))
			for _, listed := range row.Findings {
				want = append(want, configschema.Finding{
					Member:   listed.Member,
					Reason:   listed.Reason,
					Severity: severity(t, row.Name, listed.Severity),
				})
			}
			for i := range max(len(findings), len(want)) {
				switch {
				case i >= len(findings):
					t.Fatalf("%s: finding %d is missing: want %+v", row.Name, i+1, want[i])
				case i >= len(want):
					t.Fatalf("%s: finding %d is not in the corpus: got %+v", row.Name, i+1, findings[i])
				case findings[i] != want[i]:
					t.Fatalf("%s: finding %d = %+v, want %+v", row.Name, i+1, findings[i], want[i])
				}
			}
		})
	}
}

func severity(t *testing.T, name, listed string) configschema.Severity {
	t.Helper()
	switch listed {
	case "error":
		return configschema.Error
	case "warning":
		return configschema.Warning
	}
	t.Fatalf("%s: severity %q is neither error nor warning", name, listed)
	return configschema.Error
}

// Proves clause 5: a body larger than the store's config file limit is one
// Error at "$", refused before the body is parsed.
func TestCheckTooLarge(t *testing.T) {
	const limit = 1 << 20
	refused := fmt.Sprintf(`{"primary": %q}`, strings.Repeat("a", limit))
	findings, err := configschema.Check(configschema.Theme, []byte(refused))
	if err != nil {
		t.Fatalf("Check(oversized body) = %v, want no error", err)
	}
	if len(findings) != 1 {
		t.Fatalf("Check(oversized body) = %v, want one finding", findings)
	}
	if got := findings[0]; got.Member != "$" || !strings.Contains(got.Reason, "larger than") {
		t.Fatalf("Check(oversized body) = %+v, want one Error at $ naming the cap", got)
	}
	// The body at the cap parses, so a refusal of the body above it is the
	// pre-check and not the parse.
	atCap := fmt.Sprintf(`{"primary": %q}`, strings.Repeat("a", limit-len(`{"primary": ""}`)))
	if findings, err := configschema.Check(configschema.Theme, []byte(atCap)); err != nil || len(findings) != 0 {
		t.Fatalf("Check(body at the cap) = %v, %v, want no finding", findings, err)
	}
}

// Proves clause 7: an unknown kind is an error naming it, before the body is
// read, and never a panic.
func TestCheckUnknownKind(t *testing.T) {
	for _, kind := range []configschema.Kind{"nope", "", "MCP-SERVERS", "skill"} {
		findings, err := configschema.Check(kind, []byte(validBodies[configschema.Theme]))
		if err == nil {
			t.Fatalf("Check(%q) = %v, want an error", kind, findings)
		}
		if findings != nil {
			t.Fatalf("Check(%q) = %v, want no finding beside the error", kind, findings)
		}
		if !strings.Contains(err.Error(), fmt.Sprintf("%q", kind)) {
			t.Fatalf("Check(%q) error = %q, want it to name the kind", kind, err)
		}
	}
	oversized := []byte(strings.Repeat("a", 2<<20))
	if _, err := configschema.Check("nope", oversized); err == nil {
		t.Fatal("Check(nope, oversized body) = no error, want the kind refused before the body is read")
	}
}
