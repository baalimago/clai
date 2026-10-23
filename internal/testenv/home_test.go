package testenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsolateHome_pointsUserDirsIntoANewDirectory(t *testing.T) {
	for _, key := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(key, "/developer")
	}

	home, err := IsolateHome()
	if err != nil {
		t.Fatalf("IsolateHome: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(string(home)) })

	for name, dir := range map[string]func() (string, error){
		"UserHomeDir":   os.UserHomeDir,
		"UserConfigDir": os.UserConfigDir,
		"UserCacheDir":  os.UserCacheDir,
	} {
		got, err := dir()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.HasPrefix(got, string(home)+string(filepath.Separator)) && got != string(home) {
			t.Fatalf("%s = %q, want inside %q", name, got, home)
		}
	}
}
