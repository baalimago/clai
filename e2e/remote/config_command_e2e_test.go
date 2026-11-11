package remote

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/baalimago/clai/e2e/e2etest"
	"github.com/baalimago/clai/internal/configschema"
	"github.com/baalimago/clai/internal/profile"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

// TestConfigSchemaListsKinds proves criterion 1 / clause 4: `config schema`
// prints one sorted kind per line with its version.
func TestConfigSchemaListsKinds(t *testing.T) {
	confDir := e2etest.SetupMainTestConfigDir(t)
	stdout, status := harness.RunOne(t, confDir, "config schema")
	if status != 0 {
		t.Fatalf("status = %d, want 0; stdout %q", status, stdout)
	}
	want := make([]string, 0, len(configschema.Kinds())+1)
	for _, kind := range configschema.Kinds() {
		want = append(want, string(kind)+" "+configschema.Version(kind))
	}
	want = append(want, "profile "+profile.SchemaVersion())
	slices.Sort(want)
	got := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
	if !slices.Equal(got, want) {
		t.Fatalf("config schema = %v, want %v", got, want)
	}
}

// TestConfigSchemaPrintsDocument proves criterion 2 / clause 4: `config
// schema <kind>` prints the embedded document byte for byte, then a newline.
func TestConfigSchemaPrintsDocument(t *testing.T) {
	confDir := e2etest.SetupMainTestConfigDir(t)

	theme, ok := configschema.Document(configschema.Theme)
	if !ok {
		t.Fatal("the theme document is missing")
	}
	gotTheme, status := harness.RunOne(t, confDir, "config schema theme")
	if status != 0 {
		t.Fatalf("config schema theme: status = %d, want 0", status)
	}
	if want := string(theme) + "\n"; gotTheme != want {
		t.Fatalf("config schema theme = %q, want the embedded bytes plus a newline", gotTheme)
	}

	gotProfile, status := harness.RunOne(t, confDir, "config schema profile")
	if status != 0 {
		t.Fatalf("config schema profile: status = %d, want 0", status)
	}
	if want := string(profile.Schema()) + "\n"; gotProfile != want {
		t.Fatalf("config schema profile = %q, want the embedded bytes plus a newline", gotProfile)
	}
}

// TestConfigDirIsSideEffectFree proves criterion 9 / clause 9: `config dir`
// prints the config dir and creates or migrates nothing.
func TestConfigDirIsSideEffectFree(t *testing.T) {
	confDir := t.TempDir()
	t.Setenv("CLAI_CONFIG_DIR", confDir)

	before := readDirNames(t, confDir)
	stdout, status := harness.RunOne(t, confDir, "config dir")
	if status != 0 {
		t.Fatalf("status = %d, want 0; stdout %q", status, stdout)
	}
	if stdout != confDir+"\n" {
		t.Fatalf("config dir printed %q, want %q", stdout, confDir+"\n")
	}
	after := readDirNames(t, confDir)
	if !slices.Equal(before, after) {
		t.Fatalf("config dir changed the dir: before %v, after %v", before, after)
	}
	if len(after) != 0 {
		t.Fatalf("config dir created files: %v", after)
	}
}

func readDirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%q): %v", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// TestConfigEditMacro proves criterion 10 / clause 2: the documented `config
// edit` spelling drives the same wizard with the same macro stages as the
// hidden `s`.
func TestConfigEditMacro(t *testing.T) {
	canonicalDir := e2etest.SetupMainTestConfigDir(t)
	canonical, canonicalStatus := harness.RunOne(t, canonicalDir, "-n config edit 0 0 q")
	if canonicalStatus != 0 {
		t.Fatalf("config edit: status = %d, want 0; stdout %q", canonicalStatus, canonical)
	}

	aliasDir := e2etest.SetupMainTestConfigDir(t)
	alias, aliasStatus := harness.RunOne(t, aliasDir, "-n s 0 0 q")
	if aliasStatus != 0 {
		t.Fatalf("setup alias: status = %d, want 0; stdout %q", aliasStatus, alias)
	}

	canonical = strings.ReplaceAll(canonical, canonicalDir, "<config>")
	alias = strings.ReplaceAll(alias, aliasDir, "<config>")
	if canonical != alias {
		t.Fatalf("config edit and s differ:\nconfig edit:\n%s\ns:\n%s", canonical, alias)
	}
}

// TestConfigListProfile proves criterion 12 / clause 3: `config list profile`
// prints one line per file, keeps the model, tools, prompt and source stamp,
// and names a file it cannot read.
func TestConfigListProfile(t *testing.T) {
	confDir := e2etest.SetupMainTestConfigDir(t)
	profilesDir := filepath.Join(confDir, "profiles")
	// A native tool is registered without a PATH lookup, so the row answers the
	// same on a runner without the host program (ripgrep is absent on the
	// GitHub ubuntu image).
	writeProfileFile(t, profilesDir, "cody.json", `{"name":"cody","model":"test","tools":["sed"],"prompt":"You are Cody. Second sentence."}`)
	writeProfileFile(t, profilesDir, "gopher.json", `{"model":"test","tools":[],"prompt":"Be terse.","source":{"target":"cchub:ada/gopher@1"}}`)
	writeProfileFile(t, profilesDir, "broken.json", `{not-json`)

	stdout, status := harness.RunOne(t, confDir, "config list profile")
	if status != 0 {
		t.Fatalf("status = %d, want 0; stdout %q", status, stdout)
	}
	lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("config list profile printed %d lines, want 3:\n%s", len(lines), stdout)
	}
	cody := lineFor(t, lines, filepath.Join(profilesDir, "cody.json"))
	for _, want := range []string{"model=test", "tools=[sed]", `prompt="You are Cody."`} {
		if !strings.Contains(cody, want) {
			t.Fatalf("cody line %q lacks %q", cody, want)
		}
	}
	gopher := lineFor(t, lines, filepath.Join(profilesDir, "gopher.json"))
	if !strings.Contains(gopher, "source=cchub:ada/gopher@1") {
		t.Fatalf("gopher line %q lacks the source stamp", gopher)
	}
	broken := lineFor(t, lines, filepath.Join(profilesDir, "broken.json"))
	if !strings.Contains(broken, "cannot read") {
		t.Fatalf("broken line %q does not name why it cannot be read", broken)
	}
}

func writeProfileFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", name, err)
	}
}

func lineFor(t *testing.T, lines []string, path string) string {
	t.Helper()
	for _, line := range lines {
		if strings.Contains(line, path) {
			return line
		}
	}
	t.Fatalf("no line names %q in %v", path, lines)
	return ""
}

// TestConfigUnknownVerb proves clause 1's error path: an unknown verb exits 2
// and prints the verb together with the synopsis.
func TestConfigUnknownVerb(t *testing.T) {
	_ = e2etest.SetupMainTestConfigDir(t)
	var status int
	var stdout string
	stderr := testboil.CaptureStderr(t, func(t *testing.T) {
		stdout = testboil.CaptureStdout(t, func(t *testing.T) {
			status = harness.Run(strings.Split("config nope", " "))
		})
	})
	testboil.FailTestIfDiff(t, status, 2)
	combined := stdout + stderr
	for _, want := range []string{"nope", "clai config [verb] [kind] [target]"} {
		if !strings.Contains(combined, want) {
			t.Fatalf("output lacks %q: %q", want, combined)
		}
	}
}

// writeTarget writes one target body to a fresh temp file.
func writeTarget(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "target.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
	return path
}

// TestConfigValidateDispatch proves the clause that `config validate <kind>
// <target>` dispatches on the kind it is given: profile runs the profile check
// over the target grammar, every other kind applies that kind's schema
// document, and an unknown kind is refused before any target is read.
func TestConfigValidateDispatch(t *testing.T) {
	confDir := e2etest.SetupMainTestConfigDir(t)
	goodProfile := writeTarget(t, validProfileBody)
	typoProfile := writeTarget(t, `{"model":"test","prompt":"p","promt":"x"}`)
	goodTheme := writeTarget(t, `{"primary":"#fff","tableItems":3}`)
	badTheme := writeTarget(t, `{"tableItems":"many"}`)

	stdout, status := harness.RunOne(t, confDir, "config validate profile "+goodProfile)
	if status != 0 || stdout != "" {
		t.Fatalf("a clean profile: status = %d stdout %q, want 0 and no finding", status, stdout)
	}
	stdout, status = harness.RunOne(t, confDir, "config validate theme "+goodTheme)
	if status != 0 || stdout != "" {
		t.Fatalf("a clean theme: status = %d stdout %q, want 0 and no finding", status, stdout)
	}
	stdout, status = harness.RunOne(t, confDir, "config validate profile "+typoProfile)
	if status != 1 || !strings.Contains(stdout, "error promt:") || !strings.Contains(stdout, `did you mean "prompt"?`) {
		t.Fatalf("a profile typo: status = %d stdout %q, want the finding and 1", status, stdout)
	}
	// The same bytes are clean as a profile and refused as a theme: the kind
	// that was named is the one that was applied.
	stdout, status = harness.RunOne(t, confDir, "config validate theme "+badTheme)
	if status != 1 || !strings.Contains(stdout, "error tableItems:") || strings.Contains(stdout, "promt") {
		t.Fatalf("a theme typo: status = %d stdout %q, want the theme finding and 1", status, stdout)
	}
	stdout, status = harness.RunOne(t, confDir, "config validate nope "+goodTheme)
	if status != 2 || !strings.Contains(stdout, `unknown kind "nope"`) || !strings.Contains(stdout, "profile") {
		t.Fatalf("an unknown kind: status = %d stdout %q, want the kinds named and 2", status, stdout)
	}
}

// TestConfigValidateExitCodes proves the clause that fixes validate's status:
// 0 for a clean target, 1 for a finding, and 2 for an unknown kind, a missing
// target or an unreadable file, with -json printing one array and nothing else.
func TestConfigValidateExitCodes(t *testing.T) {
	_ = e2etest.SetupMainTestConfigDir(t)
	clean := writeTarget(t, `{"primary":"#fff"}`)
	finding := writeTarget(t, `{"tableItems":"many"}`)
	missing := filepath.Join(t.TempDir(), "gone.json")

	for _, tc := range []struct {
		args string
		want int
	}{
		{"config validate theme " + clean, 0},
		{"config validate theme " + finding, 1},
		{"config validate theme " + missing, 2},
		{"config validate nope " + clean, 2},
		{"config validate", 2},
		{"config validate theme", 2},
		{"config validate theme " + clean + " " + finding, 2},
	} {
		status, stdout, stderr := runRemote(t, tc.args)
		if status != tc.want {
			t.Fatalf("%s: status = %d, want %d; stdout %q stderr %q", tc.args, status, tc.want, stdout, stderr)
		}
	}

	_, stdout, stderr := runRemote(t, "config validate")
	if !strings.Contains(stdout, "usage: clai config validate <kind> <target> [-json]") {
		t.Fatalf("no target: stdout %q stderr %q, want the usage line", stdout, stderr)
	}

	status, stdout, stderr := runRemote(t, "config validate theme "+finding+" -json")
	if status != 1 {
		t.Fatalf("-json: status = %d, want 1; stderr %q", status, stderr)
	}
	dec := json.NewDecoder(strings.NewReader(stdout))
	var findings []struct {
		Severity string   `json:"severity"`
		Member   string   `json:"member"`
		Reason   string   `json:"reason"`
		Routes   []string `json:"routes"`
	}
	if err := dec.Decode(&findings); err != nil {
		t.Fatalf("-json: stdout %q is no JSON array: %v", stdout, err)
	}
	if dec.More() {
		t.Fatalf("-json: stdout %q holds more than one value", stdout)
	}
	if len(findings) != 1 || findings[0].Member != "tableItems" || findings[0].Severity != "error" || len(findings[0].Routes) != 3 {
		t.Fatalf("-json: findings = %+v, want the one tableItems error on every route", findings)
	}
}

// setupConfigPublishE2E points the Hub client at the fake hub and writes the
// profile the publish rows send.
func setupConfigPublishE2E(t *testing.T) remoteE2E {
	t.Helper()
	env := setupRemoteE2E(t)
	api := httptest.NewServer(env.hub)
	t.Cleanup(api.Close)
	t.Setenv("CLAI_CLOUD_URL", api.URL)
	t.Setenv("CLAI_CLOUD_TOKEN", fakeHubToken)
	if err := os.WriteFile(filepath.Join(env.confDir, "profiles", "trade-bot.json"), []byte(remoteBotV1), 0o644); err != nil {
		t.Fatal(err)
	}
	return env
}

// TestConfigPublishPicker proves the clause that `config publish` asks for
// each level its arguments leave out: the piped answers reach the prompts in
// order, a bare name infers its kind, and -n makes a missing answer an error
// naming what to pass.
func TestConfigPublishPicker(t *testing.T) {
	env := setupConfigPublishE2E(t)

	// Nothing given: the kind is asked, then the name.
	e2etest.WithStdin(t, []byte("profile\ntrade-bot\n"))
	status, stdout, stderr := runRemote(t, "config publish -handle ada")
	if status != 0 || !strings.Contains(stdout, "publish kind (alias|profile|skill): ") || !strings.Contains(stdout, "name: ") {
		t.Fatalf("both levels asked: status %d stdout %q stderr %q", status, stdout, stderr)
	}
	if !strings.Contains(stdout, "Published cchub:ada/trade-bot@1") {
		t.Fatalf("both levels asked: stdout %q does not name the published agent", stdout)
	}

	// The kind given, the name asked.
	e2etest.WithStdin(t, []byte("trade-bot\n"))
	status, stdout, stderr = runRemote(t, "config publish profile")
	if status != 0 || !strings.Contains(stdout, "Published cchub:ada/trade-bot@2 (a new version)") {
		t.Fatalf("name asked: status %d stdout %q stderr %q", status, stdout, stderr)
	}

	// A bare name needs no prompt: the one kind carrying it is the kind.
	status, stdout, stderr = runRemote(t, "config publish trade-bot")
	if status != 0 || !strings.Contains(stdout, "Published cchub:ada/trade-bot@3") {
		t.Fatalf("inferred kind: status %d stdout %q stderr %q", status, stdout, stderr)
	}

	// -n refuses to ask, and names the argument to pass instead.
	for args, want := range map[string]string{
		"config publish -n":         "clai config publish <alias|profile|skill>",
		"config publish -n profile": "clai config publish profile <name>",
	} {
		status, _, stderr = runRemote(t, args)
		if status != 1 || !strings.Contains(stderr, want) {
			t.Fatalf("%s: status %d stderr %q, want %q", args, status, stderr, want)
		}
	}
	if hits := env.hub.hitsOn("/api/v1/hub"); hits != 3 {
		t.Fatalf("the hub got %d publishes, want the three answered ones", hits)
	}
}

// TestConfigProfileOnlyVerbs proves the clause that trust, install and update
// exist only for kind profile: another kind exits 2 and names the operation
// that does not exist for it, and the profile spellings still run.
func TestConfigProfileOnlyVerbs(t *testing.T) {
	env := setupRemoteE2E(t)

	for args, want := range map[string]string{
		"config trust theme x":   "config trust does not exist for kind \"theme\"",
		"config install theme x": "config install does not exist for kind \"theme\"",
		"config update theme":    "config update does not exist for kind \"theme\"",
	} {
		status, stdout, stderr := runRemote(t, args)
		if status != 2 || !strings.Contains(stdout, want) {
			t.Fatalf("%s: status %d stdout %q stderr %q, want %q and 2", args, status, stdout, stderr, want)
		}
	}
	// A missing argument is a usage error, and the line names the profile
	// spelling the verb only has.
	for args, want := range map[string]string{
		"config trust":           "usage: clai config trust profile <target>",
		"config install profile": "usage: clai config install profile <target> [name]",
	} {
		status, stdout, stderr := runRemote(t, args)
		if status != 2 || !strings.Contains(stdout, want) {
			t.Fatalf("%s: status %d stdout %q stderr %q, want %q and 2", args, status, stdout, stderr, want)
		}
	}

	status, stdout, stderr := runRemote(t, "config trust profile cchub:ada/bot")
	if status != 0 {
		t.Fatalf("trust: status %d stdout %q stderr %q", status, stdout, stderr)
	}
	if env.trusted(t) == "" {
		t.Fatal("config trust profile recorded no trust")
	}
	status, stdout, stderr = runRemote(t, "config install profile cchub:ada/bot reviewer")
	if status != 0 {
		t.Fatalf("install: status %d stdout %q stderr %q", status, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(env.confDir, "profiles", "reviewer.json")); err != nil {
		t.Fatalf("install wrote no profile: %v", err)
	}
	status, stdout, stderr = runRemote(t, "config update profile reviewer")
	if status != 0 {
		t.Fatalf("update: status %d stdout %q stderr %q", status, stdout, stderr)
	}
}

// TestHiddenAliasesDelegate proves the clause that every pre-existing
// spelling stays callable: each one re-enters the config verb that documents
// it, so the output and the status are the same, and none of them is listed
// in help or offered by completion.
func TestHiddenAliasesDelegate(t *testing.T) {
	confDir := e2etest.SetupMainTestConfigDir(t)
	writeProfileFile(t, filepath.Join(confDir, "profiles"), "cody.json", `{"name":"cody","model":"test","prompt":"You are Cody."}`)
	typo := writeTarget(t, `{"model":"test","prompt":"p","promt":"x"}`)

	for _, pair := range [][2]string{
		{"profiles schema", "config schema profile"},
		{"profiles list", "config list profile"},
		{"confdir", "config dir"},
		{"profiles validate " + typo, "config validate profile " + typo},
		{"profiles trust", "config trust profile"},
		{"profiles install", "config install profile"},
	} {
		oldStatus, oldOut, oldErr := runRemote(t, pair[0])
		newStatus, newOut, newErr := runRemote(t, pair[1])
		if oldStatus != newStatus || oldOut != newOut || oldErr != newErr {
			t.Fatalf("%q and %q differ:\n%d %q %q\n%d %q %q",
				pair[0], pair[1], oldStatus, oldOut, oldErr, newStatus, newOut, newErr)
		}
	}

	// The documented spellings are listed; the hidden ones are not.
	var status int
	cloudHelp := testboil.CaptureStdout(t, func(t *testing.T) {
		status = harness.Run([]string{"cloud", "-h"})
	})
	if status != 0 {
		t.Fatalf("cloud -h: status = %d", status)
	}
	if strings.Contains(cloudHelp, "\n  publish ") {
		t.Fatalf("cloud -h lists the hidden publish spelling:\n%s", cloudHelp)
	}
	profilesHelp := testboil.CaptureStdout(t, func(t *testing.T) {
		status = harness.Run([]string{"profiles", "-h"})
	})
	if status != 0 {
		t.Fatalf("profiles -h: status = %d", status)
	}
	for _, name := range []string{"list", "validate", "schema", "install", "update", "trust", "publish"} {
		if strings.Contains(profilesHelp, "\n  "+name+" ") {
			t.Fatalf("profiles -h lists the hidden spelling %q:\n%s", name, profilesHelp)
		}
	}

	// `clai config -h` is where the verbs a person types are documented.
	configHelp := testboil.CaptureStdout(t, func(t *testing.T) {
		status = harness.Run([]string{"config", "-h"})
	})
	if status != 0 {
		t.Fatalf("config -h: status = %d", status)
	}
	for _, verb := range []string{"edit", "list", "schema", "validate", "publish", "trust", "install", "update", "dir"} {
		if !strings.Contains(configHelp, "\n  "+verb+" ") {
			t.Fatalf("config -h does not list %q:\n%s", verb, configHelp)
		}
	}
}
