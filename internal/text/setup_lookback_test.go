package text

import (
	"strings"
	"testing"

	"github.com/baalimago/clai/internal"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// Test_setupLookback_ExplicitDisableOverridesProfile pins the documented
// precedence CLI > profile in the disabling direction: -lb=false must win
// over profile/file-enabled lookback.
func Test_setupLookback_ExplicitDisableOverridesProfile(t *testing.T) {
	tConf := Configurations{UseLookback: true}
	var lookback internal.BoolFlag
	if err := lookback.Set("false"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := setupLookback(t.TempDir(), &tConf, lookback); err != nil {
		t.Fatalf("setupLookback: %v", err)
	}
	if tConf.UseLookback {
		t.Fatal("expected explicit -lb=false to disable profile-enabled lookback")
	}
	if tConf.LookbackCWD == "" {
		t.Fatal("expected LookbackCWD to be captured even when lookback is disabled")
	}
}

// Test_setupLookback_NoticeDebugGated pins the debug gate on the lookback setup
// notices: they print only when DEBUG_LOOKBACK (or plain DEBUG) is truthy, and
// never in structured-response mode. This covers the enabled-but-no-history
// branch; the with-history branch is covered by
// Test_e2e_dirscope_lookback_notice_debug_gated.
func Test_setupLookback_NoticeDebugGated(t *testing.T) {
	t.Run("silent without debug flag", func(t *testing.T) {
		t.Setenv("DEBUG", "")
		t.Setenv("DEBUG_LOOKBACK", "")
		out := captureStdout(t, func() {
			tConf := Configurations{UseLookback: true}
			var lookback internal.BoolFlag
			if err := lookback.Set("true"); err != nil {
				t.Fatalf("Set: %v", err)
			}
			if err := setupLookback(t.TempDir(), &tConf, lookback); err != nil {
				t.Fatalf("setupLookback: %v", err)
			}
		})
		if strings.Contains(out, "lookback:") {
			t.Fatalf("expected no lookback notice without DEBUG_LOOKBACK, got %q", out)
		}
	})
	t.Run("prints when any debug flag is set", func(t *testing.T) {
		for _, tt := range []struct {
			name string
			env  map[string]string
		}{
			{name: "DEBUG_LOOKBACK", env: map[string]string{"DEBUG": "", "DEBUG_LOOKBACK": "1"}},
			{name: "plain DEBUG", env: map[string]string{"DEBUG": "1", "DEBUG_LOOKBACK": ""}},
		} {
			t.Run(tt.name, func(t *testing.T) {
				for k, v := range tt.env {
					t.Setenv(k, v)
				}
				out := captureStdout(t, func() {
					tConf := Configurations{UseLookback: true}
					var lookback internal.BoolFlag
					if err := lookback.Set("true"); err != nil {
						t.Fatalf("Set: %v", err)
					}
					if err := setupLookback(t.TempDir(), &tConf, lookback); err != nil {
						t.Fatalf("setupLookback: %v", err)
					}
				})
				if !strings.Contains(out, "lookback: enabled (no recorded history") {
					t.Fatalf("expected no-history lookback notice with %s, got %q", tt.name, out)
				}
			})
		}
	})
	t.Run("silent in structured-response mode", func(t *testing.T) {
		t.Setenv("DEBUG", "1")
		t.Setenv("DEBUG_LOOKBACK", "1")
		out := captureStdout(t, func() {
			tConf := Configurations{UseLookback: true, ResponseFormat: &pub_models.ResponseFormat{}}
			var lookback internal.BoolFlag
			if err := lookback.Set("true"); err != nil {
				t.Fatalf("Set: %v", err)
			}
			if err := setupLookback(t.TempDir(), &tConf, lookback); err != nil {
				t.Fatalf("setupLookback: %v", err)
			}
		})
		if strings.Contains(out, "lookback:") {
			t.Fatalf("expected no lookback notice in structured-response mode, got %q", out)
		}
	})
}
