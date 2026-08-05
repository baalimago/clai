package text

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/text/generic"
	"github.com/baalimago/clai/internal/utils"
)

func TestResponseFormatFromGeneric_Nil(t *testing.T) {
	if got := responseFormatFromGeneric(nil); got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

func TestResponseFormatFromGeneric_JSONObject(t *testing.T) {
	gf := &generic.ResponseFormat{Type: "json_object"}
	rf := responseFormatFromGeneric(gf)
	if rf == nil {
		t.Fatal("expected non-nil")
	}
	if rf.Type != "json_object" {
		t.Fatalf("expected json_object, got %q", rf.Type)
	}
	if rf.Schema != nil {
		t.Fatal("expected nil Schema")
	}
}

func TestResponseFormatFromGeneric_JSONSchema(t *testing.T) {
	gf := &generic.ResponseFormat{
		Type: "json_schema",
		JSONSchema: &generic.JSONSchemaSpec{
			Name:        "person",
			Description: "A person record",
			Strict:      true,
			Schema: map[string]any{
				"type": "object",
			},
		},
	}
	rf := responseFormatFromGeneric(gf)
	if rf == nil {
		t.Fatal("expected non-nil")
	}
	if rf.Type != "json_schema" {
		t.Fatalf("expected json_schema, got %q", rf.Type)
	}
	if rf.Schema == nil {
		t.Fatal("expected Schema")
	}
	if rf.Schema.Name != "person" {
		t.Fatalf("expected Name=person, got %q", rf.Schema.Name)
	}
	if rf.Schema.Description != "A person record" {
		t.Fatalf("expected Description='A person record', got %q", rf.Schema.Description)
	}
	if !rf.Schema.Strict {
		t.Fatal("expected Strict=true")
	}
}

func TestLoadResponseFormat_JSONObject(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rf.json")
	if err := os.WriteFile(path, []byte(`{"type":"json_object"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var c Configurations
	if err := c.LoadResponseFormat(path); err != nil {
		t.Fatalf("LoadResponseFormat: %v", err)
	}
	if c.ResponseFormat == nil {
		t.Fatal("expected ResponseFormat")
	}
	if c.ResponseFormat.Type != "json_object" {
		t.Fatalf("expected json_object, got %q", c.ResponseFormat.Type)
	}
}

func TestLoadResponseFormat_JSONSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rf.json")
	content := `{
		"type": "json_schema",
		"json_schema": {
			"name": "person",
			"description": "A person record",
			"strict": true,
			"schema": {
				"type": "object",
				"properties": {
					"name": {"type": "string"},
					"age": {"type": "integer"}
				},
				"required": ["name", "age"]
			}
		}
	}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	var c Configurations
	if err := c.LoadResponseFormat(path); err != nil {
		t.Fatalf("LoadResponseFormat: %v", err)
	}
	if c.ResponseFormat == nil {
		t.Fatal("expected ResponseFormat")
	}
	if c.ResponseFormat.Type != "json_schema" {
		t.Fatalf("expected json_schema, got %q", c.ResponseFormat.Type)
	}
	if c.ResponseFormat.Schema == nil {
		t.Fatal("expected Schema")
	}
	if c.ResponseFormat.Schema.Name != "person" {
		t.Fatalf("expected Name=person, got %q", c.ResponseFormat.Schema.Name)
	}
}

func TestLoadResponseFormat_FileNotFound(t *testing.T) {
	var c Configurations
	err := c.LoadResponseFormat("/nonexistent/path.json")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadResponseFormat_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rf.json")
	if err := os.WriteFile(path, []byte(`not json`), 0o644); err != nil {
		t.Fatal(err)
	}
	var c Configurations
	err := c.LoadResponseFormat(path)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestConfigurations_LegacyTokenWarnLimitKeyIgnored(t *testing.T) {
	// Sunset contract (worklog 2026-08-04-token-stoploss, Phase 1, D10):
	// old textConfig.json files carrying the dead token-warn-limit key
	// load without error; encoding/json ignores the unknown key and
	// regenerated configs omit it.
	dir := t.TempDir()
	confPath := filepath.Join(dir, "textConfig.json")
	content := `{"model":"test","token-warn-limit":333333}`
	if err := os.WriteFile(confPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(textConfig.json): %v", err)
	}

	conf, err := utils.LoadConfigFromFile(dir, "textConfig.json", nil, &Default)
	if err != nil {
		t.Fatalf("LoadConfigFromFile: %v", err)
	}
	if conf.Model != "test" {
		t.Fatalf("expected model test, got %q", conf.Model)
	}

	// LoadConfigFromFile appends default fields and rewrites the file when
	// anything changed; the dead key must not survive the rewrite.
	regenerated, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatalf("ReadFile(regenerated): %v", err)
	}
	if strings.Contains(string(regenerated), "token-warn-limit") {
		t.Fatalf("regenerated config still carries token-warn-limit:\n%s", regenerated)
	}
}

func TestConfigurations_CmdBanPersistsViaTextConfigJSON(t *testing.T) {
	dir := t.TempDir()
	confPath := filepath.Join(dir, "textConfig.json")
	content := `{"model":"test","cmd-ban":["rm","sudo"]}`
	if err := os.WriteFile(confPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(textConfig.json): %v", err)
	}

	conf, err := utils.LoadConfigFromFile(dir, "textConfig.json", nil, &Default)
	if err != nil {
		t.Fatalf("LoadConfigFromFile: %v", err)
	}
	want := []string{"rm", "sudo"}
	if !slices.Equal(conf.CmdBan, want) {
		t.Fatalf("expected CmdBan %v, got %v", want, conf.CmdBan)
	}
}
