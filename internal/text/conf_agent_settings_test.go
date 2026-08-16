package text

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
)

// TestConfigurations_AgentSettings proves Configurations carries the full
// AgentSettings group: the slog logger, its level, the rune cap, and both
// recorder hooks (worklog 2026-08-15-agent-slog-output, D7).
func TestConfigurations_AgentSettings(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	usageRec := &recordingCallUsageRecorder{}
	toolRec := &recordingToolCallRecorder{}
	conf := Configurations{
		AgentSettings: &AgentSettings{
			Logger:           logger,
			Level:            slog.LevelWarn,
			RuneLimit:        42,
			UsageRecorder:    usageRec,
			ToolCallRecorder: toolRec,
		},
	}
	as := conf.AgentSettings
	if as == nil {
		t.Fatal("expected AgentSettings")
	}
	if as.Logger != logger {
		t.Errorf("Logger: got %v, want the attached logger", as.Logger)
	}
	if as.Level != slog.LevelWarn {
		t.Errorf("Level: got %v, want Warn", as.Level)
	}
	if as.RuneLimit != 42 {
		t.Errorf("RuneLimit: got %d, want 42", as.RuneLimit)
	}
	if as.UsageRecorder != usageRec {
		t.Errorf("UsageRecorder: got %v, want the attached recorder", as.UsageRecorder)
	}
	if as.ToolCallRecorder != toolRec {
		t.Errorf("ToolCallRecorder: got %v, want the attached recorder", as.ToolCallRecorder)
	}
}

// TestConfigurations_AgentSettings_jsonOmitted proves AgentSettings never
// serializes into textConfig.json or the presence-based config merge
// (json:"-", D7): a marshal/unmarshal round-trip must not leak the group and
// must leave the receiving side with a nil pointer.
func TestConfigurations_AgentSettings_jsonOmitted(t *testing.T) {
	conf := Configurations{
		Model: "mock",
		AgentSettings: &AgentSettings{
			Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
			Level:     slog.LevelDebug,
			RuneLimit: 200,
		},
	}
	data, err := json.Marshal(conf)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if bytes.Contains(data, []byte("AgentSettings")) || bytes.Contains(data, []byte("agentSettings")) {
		t.Fatalf("AgentSettings leaked into JSON: %s", data)
	}
	var decoded Configurations
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if decoded.AgentSettings != nil {
		t.Fatal("expected nil AgentSettings after round-trip")
	}
}
