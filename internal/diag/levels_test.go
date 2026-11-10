package diag_test

import (
	"strings"
	"testing"

	. "github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/testenv"
	"github.com/baalimago/clai/pkg/diagnostic"
)

func TestMain(m *testing.M) { testenv.Main(m) }

type resolverFactory struct {
	name string
	new  func() LevelResolver
}

func resolverFactories() []resolverFactory {
	return []resolverFactory{{name: "default", new: NewLevelResolver}}
}

func TestLevelResolverContract(t *testing.T) {
	for _, implementation := range resolverFactories() {
		t.Run(implementation.name, func(t *testing.T) {
			resolver := implementation.new()

			t.Run("Resolve.1_defaults_aliases_and_explicit_overrides", func(t *testing.T) {
				got, err := resolver.Resolve(LevelRequest{
					Registry: []string{"chat", "vendor.openai", "vendor.openai.stream"},
					Layers:   []LevelLayer{{"CHAT": diagnostic.LevelError}},
					LogLevel: "warning,Vendor.OpenAI=DEBUG",
				})
				assertResolved(t, got, err, ResolvedLevels{
					"":                     diagnostic.LevelWarn,
					"chat":                 diagnostic.LevelError,
					"vendor.openai":        diagnostic.LevelDebug,
					"vendor.openai.stream": diagnostic.LevelDebug,
				})
			})

			t.Run("Resolve.2_layer_precedence_and_longest_complete_selector", func(t *testing.T) {
				got, err := resolver.Resolve(LevelRequest{
					Registry: []string{"vendor.openai", "vendor.openai.stream", "vendor.openrouter"},
					Layers: []LevelLayer{
						{"": diagnostic.LevelError, "vendor": diagnostic.LevelWarn, "vendor.openai": diagnostic.LevelInfo},
						{"vendor.openai": diagnostic.LevelDebug},
					},
					LogLevel: "trace,vendor.openai.stream=error",
				})
				assertResolved(t, got, err, ResolvedLevels{
					"":                     diagnostic.LevelTrace,
					"vendor.openai":        diagnostic.LevelDebug,
					"vendor.openai.stream": diagnostic.LevelError,
					"vendor.openrouter":    diagnostic.LevelWarn,
				})
			})

			for _, test := range []struct {
				name    string
				request LevelRequest
				entry   string
			}{
				{name: "malformed", request: LevelRequest{Registry: []string{"chat"}, LogLevel: "info,chat"}, entry: "chat"},
				{name: "missing_default", request: LevelRequest{Registry: []string{"chat"}, LogLevel: "chat=debug"}, entry: "chat=debug"},
				{name: "duplicate", request: LevelRequest{Registry: []string{"chat"}, LogLevel: "info,chat=debug,CHAT=trace"}, entry: "CHAT=trace"},
				{name: "invalid_level", request: LevelRequest{Registry: []string{"chat"}, LogLevel: "loud"}, entry: "loud"},
				{name: "unknown_selector", request: LevelRequest{Registry: []string{"chat"}, LogLevel: "info,other=debug"}, entry: "other=debug"},
				{name: "partial_segment", request: LevelRequest{Registry: []string{"chat"}, LogLevel: "info,hat=debug"}, entry: "hat=debug"},
				{name: "invalid_layer_level", request: LevelRequest{Registry: []string{"chat"}, Layers: []LevelLayer{{"chat": diagnostic.Level(99)}}}, entry: "chat"},
				{name: "unknown_layer_selector", request: LevelRequest{Registry: []string{"chat"}, Layers: []LevelLayer{{"other": diagnostic.LevelDebug}}}, entry: "other"},
			} {
				t.Run("Resolve.3_"+test.name, func(t *testing.T) {
					got, err := resolver.Resolve(test.request)
					if err == nil || !strings.Contains(err.Error(), test.entry) {
						t.Fatalf("Resolve.3: Resolve() error = %v, want error identifying %q", err, test.entry)
					}
					if got != nil {
						t.Fatalf("Resolve.3: invalid configuration returned usable policy %#v", got)
					}
				})
			}
		})
	}
}

func TestLegacyLevelMapping(t *testing.T) {
	const truthy = " true "

	subsystems := []string{
		"call", "chat", "claudified_msgs", "cost_manager", "dirscope", "lookback",
		"openrouter_model_catalog", "profiles", "reply_mode", "skills", "stoploss",
		"summary", "text_querier", "tools_registry_set", "verbose",
	}
	vendors := []string{"openai", "openrouter", "mistral", "huggingface", "anthropic"}

	for _, implementation := range resolverFactories() {
		t.Run(implementation.name, func(t *testing.T) {
			resolver := implementation.new()

			t.Run("Resolve.2_F9_NO_WARNINGS", func(t *testing.T) {
				assertLegacyLevel(t, resolver, []string{"chat"}, map[string]string{"NO_WARNINGS": truthy}, "", diagnostic.LevelError)
			})
			t.Run("Resolve.2_F9_DEBUG", func(t *testing.T) {
				assertLegacyLevel(t, resolver, []string{"chat"}, map[string]string{"DEBUG": truthy}, "", diagnostic.LevelDebug)
			})
			for _, subsystem := range subsystems {
				key := "DEBUG_" + strings.ToUpper(subsystem)
				t.Run("Resolve.2_F9_"+key, func(t *testing.T) {
					assertLegacyLevel(t, resolver, []string{subsystem}, map[string]string{key: truthy}, subsystem, diagnostic.LevelDebug)
				})
			}
			for _, vendor := range vendors {
				key := "DEBUG_" + strings.ToUpper(vendor)
				if vendor == "anthropic" {
					key = "ANTHROPIC_DEBUG"
				}
				t.Run("Resolve.2_F9_"+key, func(t *testing.T) {
					selector := "vendor." + vendor
					assertLegacyLevel(t, resolver, []string{selector}, map[string]string{key: truthy}, selector, diagnostic.LevelDebug)
				})
			}
			t.Run("Resolve.2_F9_LOG_LEVEL_SKILLS", func(t *testing.T) {
				assertLegacyLevel(t, resolver, []string{"skills"}, map[string]string{"LOG_LEVEL_SKILLS": "warning"}, "skills", diagnostic.LevelWarn)
			})

			for _, key := range []string{"DEBUG_CPU", "DEBUG_OUTPUT_FILE", "NO_COLOR", "SILENT", "ANCLI_NEWLINE"} {
				t.Run("Resolve.2_F9_non_level_"+key, func(t *testing.T) {
					assertLegacyLevel(t, resolver, []string{"chat"}, map[string]string{key: truthy}, "", diagnostic.LevelInfo)
				})
			}

			t.Run("Resolve.2_F9_row_order_and_LOG_LEVEL_precedence", func(t *testing.T) {
				got, err := resolver.Resolve(LevelRequest{
					Registry: []string{"skills"},
					Legacy: map[string]string{
						"NO_WARNINGS":      truthy,
						"DEBUG":            truthy,
						"DEBUG_SKILLS":     truthy,
						"LOG_LEVEL_SKILLS": "error",
					},
					LogLevel: "warn,skills=trace",
				})
				assertResolved(t, got, err, ResolvedLevels{"": diagnostic.LevelWarn, "skills": diagnostic.LevelTrace})
			})

			t.Run("Resolve.3_F9_unlisted_legacy_key", func(t *testing.T) {
				got, err := resolver.Resolve(LevelRequest{Registry: []string{"chat"}, Legacy: map[string]string{"DEBUG_UNKNOWN": truthy}})
				if err == nil || !strings.Contains(err.Error(), "DEBUG_UNKNOWN") {
					t.Fatalf("Resolve.3: Resolve() error = %v, want error naming DEBUG_UNKNOWN", err)
				}
				if got != nil {
					t.Fatalf("Resolve.3: unlisted key returned usable policy %#v", got)
				}
			})
		})
	}
}

func assertLegacyLevel(t *testing.T, resolver LevelResolver, registry []string, legacy map[string]string, selector string, want diagnostic.Level) {
	t.Helper()
	got, err := resolver.Resolve(LevelRequest{Registry: registry, Legacy: legacy})
	if err != nil {
		t.Fatalf("Resolve.2: Resolve() error = %v", err)
	}
	if got[selector] != want {
		t.Fatalf("Resolve.2: level for %q = %v, want %v (policy %#v)", selector, got[selector], want, got)
	}
}

func assertResolved(t *testing.T, got ResolvedLevels, err error, want ResolvedLevels) {
	t.Helper()
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("Resolve() = %#v, want %#v", got, want)
	}
	for selector, level := range want {
		if got[selector] != level {
			t.Errorf("Resolve()[%q] = %v, want %v", selector, got[selector], level)
		}
	}
}

// TestLegacyControls is R2-03: one list of the Legacy controls, read by the
// CLI and the test Env alike, holds exactly the keys Resolve accepts.
func TestLegacyControls(t *testing.T) {
	env := map[string]string{
		"DEBUG_CALL": "1", "ANTHROPIC_DEBUG": "1", "LOG_LEVEL_SKILLS": "debug", "NO_COLOR": "true",
		"DEBUG_NOT_A_CONTROL": "1", "LOG_LEVEL": "trace", "DEBUG_OPENAI": "",
	}
	got := LegacyControls(func(key string) string { return env[key] })
	want := map[string]string{"DEBUG_CALL": "1", "ANTHROPIC_DEBUG": "1", "LOG_LEVEL_SKILLS": "debug", "NO_COLOR": "true"}
	if len(got) != len(want) {
		t.Fatalf("LegacyControls = %v, want %v", got, want)
	}
	for key, value := range want {
		if got[key] != value {
			t.Fatalf("LegacyControls = %v, want %v", got, want)
		}
	}
	all := LegacyControls(func(string) string { return "1" })
	if len(all) < 20 {
		t.Fatalf("LegacyControls read %d keys, want every control", len(all))
	}
	all["LOG_LEVEL_SKILLS"] = "debug"
	if _, err := NewLevelResolver().Resolve(LevelRequest{Registry: []string{"call", "chat", "claudified_msgs", "cost_manager", "dirscope", "lookback", "openrouter_model_catalog", "profiles", "reply_mode", "skills", "stoploss", "summary", "text_querier", "tools_registry_set", "verbose", "vendor.openai", "vendor.openrouter", "vendor.mistral", "vendor.huggingface", "vendor.anthropic"}, Legacy: all}); err != nil {
		t.Fatalf("Resolve rejects a key LegacyControls reads: %v", err)
	}
}
