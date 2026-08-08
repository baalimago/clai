package debugflags

import (
	"strings"
	"testing"
)

func TestEnabled(t *testing.T) {
	tests := []struct {
		name      string
		env       map[string]string
		subsystem string
		want      bool
	}{
		{name: "nothing set stays disabled", env: nil, subsystem: "LOOKBACK", want: false},
		{name: "plain DEBUG enables every subsystem", env: map[string]string{"DEBUG": "1"}, subsystem: "LOOKBACK", want: true},
		{name: "feature flag enables its subsystem", env: map[string]string{"DEBUG_LOOKBACK": "1"}, subsystem: "LOOKBACK", want: true},
		{name: "subsystem name is case-insensitive", env: map[string]string{"DEBUG_LOOKBACK": "1"}, subsystem: "lookback", want: true},
		{name: "feature flag does not leak to other subsystems", env: map[string]string{"DEBUG_LOOKBACK": "1"}, subsystem: "CHAT", want: false},
		{name: "falsy values stay disabled", env: map[string]string{"DEBUG_LOOKBACK": "false"}, subsystem: "LOOKBACK", want: false},
		{name: "plain DEBUG wins over a falsy feature flag", env: map[string]string{"DEBUG": "true", "DEBUG_LOOKBACK": "false"}, subsystem: "LOOKBACK", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DEBUG", "")
			t.Setenv("DEBUG_"+strings.ToUpper(tt.subsystem), "")
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			if got := Enabled(tt.subsystem); got != tt.want {
				t.Fatalf("Enabled(%q) = %v, want %v", tt.subsystem, got, tt.want)
			}
		})
	}
}

func TestEnabledEnv(t *testing.T) {
	t.Setenv("DEBUG", "")
	t.Setenv("OPENAI_DEBUG", "")
	if EnabledEnv("OPENAI_DEBUG") {
		t.Fatal("expected disabled without a debug flag")
	}
	t.Setenv("OPENAI_DEBUG", "1")
	if !EnabledEnv("OPENAI_DEBUG") {
		t.Fatal("expected compatibility variable to enable debugging")
	}
	t.Setenv("OPENAI_DEBUG", "")
	t.Setenv("DEBUG", "1")
	if !EnabledEnv("OPENAI_DEBUG") {
		t.Fatal("expected global DEBUG to enable debugging")
	}
}

func TestOutputFile(t *testing.T) {
	t.Setenv("DEBUG_OUTPUT_FILE", "trace.json")
	if got := OutputFile(); got != "trace.json" {
		t.Fatalf("OutputFile() = %q, want trace.json", got)
	}
}
