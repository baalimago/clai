package skills

import "testing"

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want LogLevel
	}{
		{name: "empty defaults to info", in: "", want: LogLevelInfo},
		{name: "info", in: "info", want: LogLevelInfo},
		{name: "warn", in: "warn", want: LogLevelWarn},
		{name: "warning", in: "warning", want: LogLevelWarn},
		{name: "error", in: "error", want: LogLevelError},
		{name: "case insensitive", in: "WARN", want: LogLevelWarn},
		{name: "unknown defaults to info", in: "banana", want: LogLevelInfo},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseLogLevel(tt.in); got != tt.want {
				t.Fatalf("parseLogLevel(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestDebugSkills(t *testing.T) {
	for _, env := range []string{"DEBUG", "DEBUG_SKILL", "DEBUG_SKILLS"} {
		t.Run(env, func(t *testing.T) {
			t.Setenv(env, "true")
			if !debugSkills() {
				t.Fatalf("expected %s to enable skill debugging", env)
			}
		})
	}
}
