package setup

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/go_away_boilerplate/pkg/table"
)

func TestActionString_PasteAndUnknown(t *testing.T) {
	if got := pasteConfig.String(); got != "ctrl-[v] config" {
		t.Fatalf("pasteConfig.String() = %q", got)
	}
	if got := action(200).String(); got != "unset" {
		t.Fatalf("unknown action must stringify as unset, got %q", got)
	}
}

func TestActionToTableAction_NavigationClosures(t *testing.T) {
	if err := actionToTableAction[back].Action(); !errors.Is(err, table.ErrBack) {
		t.Fatalf("back action err = %v, want ErrBack", err)
	}
	if err := actionToTableAction[quit].Action(); !errors.Is(err, table.ErrUserInitiatedExit) {
		t.Fatalf("quit action err = %v, want ErrUserInitiatedExit", err)
	}
}

func TestGetConfigs_BadGlobPattern(t *testing.T) {
	_, err := getConfigs("[", nil)
	if err == nil {
		t.Fatal("expected an error for a malformed glob pattern")
	}
	if !strings.Contains(err.Error(), "failed to glob pattern") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSetupCustomTableActions_UnknownAction(t *testing.T) {
	got := setupCustomTableActions(setupCategory{
		name:              "mystery",
		itemSelectActions: []action{action(200)},
	})
	if len(got) != 0 {
		t.Fatalf("unknown actions must be skipped, got %#v", got)
	}
}

func TestSetupCustomTableActions_NewActionError(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	restore := useReadUserInputForTests(singleInput("prof"))
	defer restore()

	actions := setupCustomTableActions(setupCategory{
		name:              "model files",
		subdirPath:        filepath.Join(blocker, "models"),
		defaultConfig:     defaultMcpServer,
		itemSelectActions: []action{newaction},
	})
	if len(actions) != 1 {
		t.Fatalf("expected one custom action, got %#v", actions)
	}
	if err := actions[0].Action(); err == nil {
		t.Fatal("expected the create-new action to fail when the subdir cannot be created")
	}
}

func TestSetupCustomTableActions_PasteAction(t *testing.T) {
	withStdin(t, "")
	actions := setupCustomTableActions(setupCategory{
		name:              "mcp",
		subdirPath:        t.TempDir(),
		itemSelectActions: []action{pasteConfig},
	})
	if len(actions) != 1 {
		t.Fatalf("expected one custom action, got %#v", actions)
	}
	err := actions[0].Action()
	if err == nil || !strings.Contains(err.Error(), "failed to paste mcp server config") {
		t.Fatalf("err = %v, want the paste failure", err)
	}
}

func TestPasteMcpServerConfig_Errors(t *testing.T) {
	t.Run("empty input", func(t *testing.T) {
		withStdin(t, "\n")
		_, err := pasteMcpServerConfig(t.TempDir())
		if err == nil || !strings.Contains(err.Error(), "no configuration provided") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("malformed input", func(t *testing.T) {
		withStdin(t, "not json\n")
		_, err := pasteMcpServerConfig(t.TempDir())
		if err == nil || !strings.Contains(err.Error(), "failed to parse mcp server") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("read error", func(t *testing.T) {
		dir := t.TempDir()
		f, err := os.Open(dir)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		t.Cleanup(func() { _ = f.Close() })
		old := os.Stdin
		os.Stdin = f
		t.Cleanup(func() { os.Stdin = old })

		_, err = pasteMcpServerConfig(dir)
		if err == nil || !strings.Contains(err.Error(), "error reading input") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("valid input", func(t *testing.T) {
		confDir := t.TempDir()
		withStdin(t, `{"mcpServers":{"filesystem":{"command":"npx","args":["-y","srv"]}}}`+"\nEOF\n")
		got, err := pasteMcpServerConfig(confDir)
		if err != nil {
			t.Fatalf("pasteMcpServerConfig: %v", err)
		}
		if len(got) != 1 || got[0].name != "filesystem" {
			t.Fatalf("unexpected configs: %#v", got)
		}
		if _, err := os.Stat(filepath.Join(confDir, "filesystem.json")); err != nil {
			t.Fatalf("expected the server config written: %v", err)
		}
	})
}

func TestActionPasteMcpServer_ConfiguresPastedServer(t *testing.T) {
	dir := t.TempDir()
	withStdin(t, `{"mcpServers":{"filesystem":{"command":"npx","args":["-y","server-filesystem"],"env":{"ROOT":"/workspace"}}}}`+"\nEOF\n")
	restore := useReadUserInputForTests(singleInput("d"))
	defer restore()

	if err := actionPasteMcpServer(dir); err != nil {
		t.Fatalf("actionPasteMcpServer: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "filesystem.json"))
	if err != nil {
		t.Fatalf("read configured server: %v", err)
	}
	var got struct {
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode configured server: %v", err)
	}
	if got.Command != "npx" || strings.Join(got.Args, " ") != "-y server-filesystem" || got.Env["ROOT"] != "/workspace" {
		t.Fatalf("configured server = %#v, want pasted command, args, and environment", got)
	}
}

func TestSelectConfigItem_NoConfigs(t *testing.T) {
	err := selectConfigItem(setupCategory{name: "empty"}, nil)
	if err == nil || !strings.Contains(err.Error(), "found no configuration files") {
		t.Fatalf("err = %v", err)
	}
}

func TestActionsWithNavigation_SkipsExistingNavigation(t *testing.T) {
	got := actionsWithNavigation([]action{back, quit})
	if len(got) != 2 {
		t.Fatalf("expected navigation actions not to be appended twice, got %#v", got)
	}
}

func TestExecuteConfigAction_CopyError(t *testing.T) {
	restore := useReadUserInputForTests(singleInput("copy-name"))
	defer restore()

	err := executeConfigAction(config{name: "missing", filePath: filepath.Join(t.TempDir(), "missing.json")}, copyAction)
	if err == nil || !strings.Contains(err.Error(), "failed to copy config") {
		t.Fatalf("err = %v", err)
	}
}

func TestCreateConfigFile_MkdirAllError(t *testing.T) {
	requireWritablePerms(t)
	parent := t.TempDir()
	chmodSetupTest(t, parent, 0o500)

	_, err := createConfigFile(filepath.Join(parent, "models"), "models", defaultMcpServer)
	if err == nil || !strings.Contains(err.Error(), "failed to create models directory") {
		t.Fatalf("err = %v", err)
	}
}

func singleInput(value string) func() (string, error) {
	used := false
	return func() (string, error) {
		if used {
			return "", io.EOF
		}
		used = true
		return value, nil
	}
}

func withStdin(t *testing.T, content string) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("WriteString: %v", err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("Seek: %v", err)
	}
	old := os.Stdin
	os.Stdin = f
	t.Cleanup(func() {
		os.Stdin = old
		_ = f.Close()
	})
}

func requireWritablePerms(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("permission bits do not block root")
	}
}

func chmodSetupTest(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("Chmod(%q, %o): %v", path, mode, err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o700) })
}
