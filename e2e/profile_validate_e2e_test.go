package e2e

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/baalimago/clai/e2e/e2etest"
	"github.com/baalimago/clai/internal/remote"
	"github.com/baalimago/go_away_boilerplate/pkg/cmd"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

const validProfileBody = `{"model":"test","prompt":"You are a bot."}`

func writeProfileBody(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
	return path
}

// assertNoConversation fails unless the refused run saved nothing.
func assertNoConversation(t *testing.T, confDir string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(confDir, "conversations"))
	if err != nil {
		t.Fatalf("ReadDir(conversations): %v", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			t.Fatalf("a refused run saved the conversation %q", entry.Name())
		}
	}
}

func Test_e2e_profile_validate_valid(t *testing.T) {
	_ = e2etest.SetupMainTestConfigDir(t)
	path := writeProfileBody(t, validProfileBody)
	status, stdout, stderr := runRemote(t, "profiles validate "+path)
	if status != 0 || stdout != "" {
		t.Fatalf("a valid body: status %d stdout %q stderr %q, want no finding and 0", status, stdout, stderr)
	}
}

func Test_e2e_profile_validate_near_miss(t *testing.T) {
	confDir := e2etest.SetupMainTestConfigDir(t)
	path := writeProfileBody(t, `{"model":"test","prompt":"You are a bot.","promt":"x"}`)
	status, stdout, stderr := runRemote(t, "profiles validate "+path)
	if status != 1 {
		t.Fatalf("status = %d, want 1; stdout %q stderr %q", status, stdout, stderr)
	}
	for _, want := range []string{"error promt:", `did you mean "prompt"?`, "[*]"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout %q lacks %q", stdout, want)
		}
	}
	if strings.Contains(stdout, "You are a bot.") {
		t.Fatalf("validate ran the profile: %q", stdout)
	}
	assertNoConversation(t, confDir)
}

func Test_e2e_profile_validate_json(t *testing.T) {
	_ = e2etest.SetupMainTestConfigDir(t)
	for _, body := range []string{
		`{"model":"test","prompt":"p","promt":"x"}`,
		`{"model":"test","tools":"rg"}`,
	} {
		path := writeProfileBody(t, body)
		status, stdout, stderr := runRemote(t, "profiles validate "+path+" -json")
		if status != 1 {
			t.Fatalf("%s: status = %d, want 1; stderr %q", body, status, stderr)
		}
		dec := json.NewDecoder(strings.NewReader(stdout))
		var findings []struct {
			Severity string   `json:"severity"`
			Member   string   `json:"member"`
			Reason   string   `json:"reason"`
			Routes   []string `json:"routes"`
		}
		if err := dec.Decode(&findings); err != nil {
			t.Fatalf("%s: stdout %q is no JSON array: %v", body, stdout, err)
		}
		if dec.More() {
			t.Fatalf("%s: stdout %q holds more than one value", body, stdout)
		}
		if len(findings) == 0 || findings[0].Severity != "error" || findings[0].Member == "" {
			t.Fatalf("%s: findings = %+v", body, findings)
		}
		if len(findings[0].Routes) != 3 {
			t.Fatalf("%s: routes = %v, want every route named", body, findings[0].Routes)
		}
	}
}

func Test_e2e_profile_schema(t *testing.T) {
	_ = e2etest.SetupMainTestConfigDir(t)
	want, err := os.ReadFile(filepath.Join(e2etest.ModuleRoot(t), "internal", "profile", "schema.json"))
	if err != nil {
		t.Fatalf("ReadFile(schema.json): %v", err)
	}
	var status int
	var stdout string
	stderr := testboil.CaptureStderr(t, func(t *testing.T) {
		stdout = testboil.CaptureStdout(t, func(t *testing.T) {
			status = harness.Run([]string{"profiles", "schema"})
		})
	})
	if status != 0 || stderr != "" {
		t.Fatalf("status %d stderr %q, want 0 and no complaint", status, stderr)
	}
	// `profiles schema` now dispatches through `config schema profile`, which
	// prints the embedded bytes and then the newline every schema verb adds.
	if !bytes.Equal([]byte(stdout), append(slices.Clone(want), '\n')) {
		t.Fatalf("schema output differs from the embedded document plus a newline:\n%s", stdout)
	}
}

func Test_e2e_prp_file_refused(t *testing.T) {
	confDir := e2etest.SetupMainTestConfigDir(t)
	path := writeProfileBody(t, `{"model":"test","prompt":"You are a bot.","promt":"x"}`)
	status, stdout, stderr := runRemote(t, "-r -prp "+path+" q hello")
	if status != 1 || stdout != "" {
		t.Fatalf("status %d stdout %q stderr %q, want a refusal and 1", status, stdout, stderr)
	}
	if !strings.Contains(stderr, path+" is not a valid profile") || !strings.Contains(stderr, "prompt") {
		t.Fatalf("stderr %q does not report the refusal", stderr)
	}
	assertNoConversation(t, confDir)
}

func Test_e2e_prp_remote_refused(t *testing.T) {
	env := setupRemoteE2E(t)
	env.hub.serveFile("/gh/ada/bad/HEAD/agent.json", "application/json; charset=utf-8", `{"model":"test","prompt":"You are bot.","promt":"x"}`)
	status, stdout, stderr := runRemote(t, "-r -prp gh:ada/bad q hello")
	if status != remote.ExitRefused || stdout != "" {
		t.Fatalf("status %d stdout %q stderr %q, want %d and no output", status, stdout, stderr, remote.ExitRefused)
	}
	if !strings.Contains(stderr, "did you mean \"prompt\"?") {
		t.Fatalf("stderr %q does not name the member meant", stderr)
	}
	if env.trusted(t) != "" {
		t.Fatalf("a refused body recorded trust: %s", env.trusted(t))
	}
	// A target that will not fetch is a failed run, not a refusal: the
	// fetch error, exit 1, and still no trust record.
	status, stdout, stderr = runRemote(t, "-r -prp gh:ada/gone q hello")
	if status != 1 || stdout != "" {
		t.Fatalf("a missing target: status %d stdout %q stderr %q, want 1 and no output", status, stdout, stderr)
	}
	if !strings.Contains(stderr, "gh:ada/gone") {
		t.Fatalf("stderr %q does not name the target that would not fetch", stderr)
	}
	if env.trusted(t) != "" {
		t.Fatalf("a target that would not fetch recorded trust: %s", env.trusted(t))
	}
}

func Test_e2e_profile_validate_remote(t *testing.T) {
	env := setupRemoteE2E(t)
	env.hub.publish("ada/bad", `{"model":"test","prompt":"You are bot.","promt":"x"}`)
	status, stdout, stderr := runRemote(t, "profiles validate cchub:ada/bad")
	if status != 1 {
		t.Fatalf("status = %d, want 1; stdout %q stderr %q", status, stdout, stderr)
	}
	for _, want := range []string{"error promt:", `did you mean "prompt"?`, "[*]"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout %q lacks %q", stdout, want)
		}
	}
	if env.hub.hitsOn("/hub/ada/bad/raw") != 1 {
		t.Fatalf("validate fetched the target %d times, want 1", env.hub.hitsOn("/hub/ada/bad/raw"))
	}
	if env.trusted(t) != "" {
		t.Fatalf("validate recorded trust: %s", env.trusted(t))
	}
}

func Test_e2e_prp_quiet(t *testing.T) {
	_ = e2etest.SetupMainTestConfigDir(t)
	path := writeProfileBody(t, `{"model":"test","prompt":"You are quiet.","use_tools":false,"tools":["*"]}`)
	status, stdout, stderr := runRemote(t, "-r -prp "+path+" q hi")
	if status != 0 || stdout != "hi\n" {
		t.Fatalf("status %d stdout %q stderr %q, want the run", status, stdout, stderr)
	}
	if strings.Contains(stdout+stderr, "warning") {
		t.Fatalf("a run printed a warning: stdout %q stderr %q", stdout, stderr)
	}
}

func Test_e2e_publish_refused(t *testing.T) {
	env := setupRemoteE2E(t)
	t.Setenv("CLAI_CLOUD_TOKEN", fakeHubToken)
	body := `{"model":"test","prompt":"You are bot.","shell_context":"git"}`
	if err := os.WriteFile(filepath.Join(env.confDir, "profiles", "bot.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	status, stdout, stderr := runRemote(t, "profiles publish bot")
	if status != 1 {
		t.Fatalf("status = %d, want 1; stdout %q stderr %q", status, stdout, stderr)
	}
	if !strings.Contains(stderr, "shell_context") {
		t.Fatalf("stderr %q does not say why", stderr)
	}
	if hits := env.hub.hitsOn("/api/v1/hub"); hits != 0 {
		t.Fatalf("publish sent %d requests before refusing", hits)
	}
}

func Test_e2e_profile_validate_usage(t *testing.T) {
	_ = e2etest.SetupMainTestConfigDir(t)
	status, stdout, stderr := runRemote(t, "profiles validate")
	// The hidden spelling delegates to `config validate profile`, which owns
	// the documented usage line.
	if status != 2 || !strings.Contains(stdout, "usage: clai config validate <kind> <target> [-json]") {
		t.Fatalf("no target: status %d stdout %q stderr %q, want a usage line and 2", status, stdout, stderr)
	}
	good := writeProfileBody(t, validProfileBody)
	bad := writeProfileBody(t, `{"model":"test","prompt":"p","promt":"x"}`)
	status, stdout, stderr = runRemote(t, "profiles validate "+good+" "+bad)
	if status != 2 || !strings.Contains(stdout, "unexpected argument") || !strings.Contains(stdout, bad) || strings.Contains(stdout, "error ") {
		t.Fatalf("second target: status %d stdout %q stderr %q, want a usage line naming %q and 2", status, stdout, stderr, bad)
	}
	status, stdout, stderr = runRemote(t, "profiles validate "+good+" -bogus")
	if status != 2 || !strings.Contains(stdout, "-bogus") || strings.Contains(stderr, "flag provided") {
		t.Fatalf("unknown flag: status %d stdout %q stderr %q, want a usage line naming -bogus and 2", status, stdout, stderr)
	}
	status, stdout, stderr = runRemote(t, "profiles validate no-such-profile")
	// A target that is not there is a usage error, not a finding: exit 2.
	if status != 2 || !strings.Contains(stderr, "no such profile: no-such-profile") {
		t.Fatalf("unknown name: status %d stdout %q stderr %q, want 2 and no such profile", status, stdout, stderr)
	}
}

func Test_e2e_profile_validate_readsDiskWithoutMigrating(t *testing.T) {
	confDir := e2etest.SetupMainTestConfigDir(t)
	body := `{"prompt":"p","promt":"x"}`
	path := filepath.Join(confDir, "profiles", "typo.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	status, stdout, stderr := runRemote(t, "profiles validate typo")
	if status != 1 || !strings.Contains(stdout, "error promt:") {
		t.Fatalf("status %d stdout %q stderr %q, want the typo and 1", status, stdout, stderr)
	}
	if strings.Contains(stdout, "added new field") {
		t.Fatalf("validate migrated the target: stdout %q", stdout)
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != body {
		t.Fatalf("validate rewrote the target:\n%s", onDisk)
	}
}

func Test_e2e_profiles_list_broken(t *testing.T) {
	confDir := e2etest.SetupMainTestConfigDir(t)
	profilesDir := filepath.Join(confDir, "profiles")
	if err := os.WriteFile(filepath.Join(profilesDir, "good.json"), []byte(validProfileBody), 0o644); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(profilesDir, "broken.json")
	if err := os.WriteFile(broken, []byte("{not-json"), 0o644); err != nil {
		t.Fatal(err)
	}
	refused := filepath.Join(profilesDir, "refused.json")
	if err := os.WriteFile(refused, []byte(`{"model":"test","prompt":"p","promt":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	status, stdout, stderr := runRemote(t, "profiles")
	if status != 0 {
		t.Fatalf("status %d stderr %q, want 0", status, stderr)
	}
	if !strings.Contains(stderr, broken) {
		t.Fatalf("stderr %q does not name the unreadable file", stderr)
	}
	if !strings.Contains(stderr, "clai profiles validate "+broken) {
		t.Fatalf("stderr %q does not name the command that explains it", stderr)
	}
	if !strings.Contains(stderr, refused) || !strings.Contains(stderr, "clai profiles validate "+refused) {
		t.Fatalf("stderr %q does not name the refused profile with its validate command", stderr)
	}
	if strings.Contains(stdout, "Cannot read") {
		t.Fatalf("stdout %q carries a diagnostic", stdout)
	}
	if strings.Contains(profileLine(t, stdout, refused), "model=") {
		t.Fatalf("stdout %q lists a refused profile as healthy", stdout)
	}
	if !strings.Contains(profileLine(t, stdout, filepath.Join(profilesDir, "good.json")), "model=test") {
		t.Fatalf("stdout %q does not list the readable profile", stdout)
	}
}

func TestProfilesHelpListsSubcommands(t *testing.T) {
	_ = e2etest.SetupMainTestConfigDir(t)
	profilesCommand, ok := harness.Commands()["profiles"]
	if !ok {
		t.Fatal("no profiles command")
	}
	help := cmd.HelpText(profilesCommand)
	completion := completeLinesE2E(t, "profiles", "")
	// Every profiles verb now delegates to the config tree, so each one is a
	// hidden spelling: callable, and absent from the help table and the
	// completion list. The prose still names them, which is what a person
	// migrating their muscle memory reads.
	parent, ok := profilesCommand.(cmd.Subcommander)
	if !ok {
		t.Fatal("profiles has no subcommands")
	}
	subs := parent.Subcommands()
	for _, name := range []string{"list", "validate", "schema", "install", "update", "trust", "publish"} {
		sub, ok := subs[name]
		if !ok {
			t.Fatalf("no profiles subcommand %q to keep callable", name)
		}
		if !cmd.IsHidden(sub) {
			t.Fatalf("profiles %s is not hidden; it is the spelling of a config verb now", name)
		}
		if slices.ContainsFunc(completion, func(line string) bool { return strings.HasPrefix(line, name+"\t") }) {
			t.Fatalf("completion %v still offers the hidden spelling %q", completion, name)
		}
		if strings.Contains(help, "\n  "+name+" ") {
			t.Fatalf("profiles -h lists the hidden spelling %q:\n%s", name, help)
		}
	}
	for _, name := range []string{"validate", "schema", "install", "update", "trust", "publish"} {
		if !strings.Contains(help, name) {
			t.Fatalf("profiles -h does not name %q:\n%s", name, help)
		}
	}
}
