package procenv

import (
	"testing"

	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/testenv"
	"github.com/baalimago/clai/pkg/diagnostic"
)

func TestMain(m *testing.M) { testenv.Main(m) }

// TestEnvLevelsFollowTheCLIResolution is R2-03: Env resolves its levels
// through the real resolver over the shared Legacy controls list.
func TestEnvLevelsFollowTheCLIResolution(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		env := Env()
		if env.Enabled(runenv.SubsystemCall, diagnostic.LevelDebug) || !env.Enabled(runenv.SubsystemCall, diagnostic.LevelWarn) {
			t.Fatal("default levels show call debug or hide call warnings")
		}
	})
	t.Run("legacy control", func(t *testing.T) {
		t.Setenv("DEBUG_CALL", "1")
		env := Env()
		if !env.Enabled(runenv.SubsystemCall, diagnostic.LevelDebug) || env.Enabled(runenv.SubsystemChat, diagnostic.LevelDebug) {
			t.Fatal("DEBUG_CALL did not raise call alone to debug")
		}
	})
	t.Run("LOG_LEVEL replaces the legacy entry of its selector", func(t *testing.T) {
		t.Setenv("DEBUG_CALL", "1")
		t.Setenv("LOG_LEVEL", "warn,call=error")
		if Env().Enabled(runenv.SubsystemCall, diagnostic.LevelWarn) {
			t.Fatal("LOG_LEVEL warn,call=error did not replace DEBUG_CALL")
		}
	})
	t.Run("rejected environment", func(t *testing.T) {
		t.Setenv("LOG_LEVEL", "loud")
		env := Env()
		if !env.Enabled(runenv.SubsystemCall, diagnostic.LevelInfo) || env.Enabled(runenv.SubsystemCall, diagnostic.LevelDebug) {
			t.Fatal("a rejected environment does not leave every subsystem at info")
		}
	})
}
