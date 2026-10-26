package text

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadResponseFormat_keepsPropertyOrder pins that the schema's declared
// property order survives the -rf file -> Configurations -> vendor path.
// Models answer in schema order; an alphabetised schema changed the answers.
func TestLoadResponseFormat_keepsPropertyOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "verdict.json")
	content := `{"type":"json_schema","json_schema":{"name":"verdict","schema":{"type":"object","properties":{"ship":{"type":"boolean"},"reason":{"type":"string"}},"required":["ship","reason"]}}}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	var c Configurations
	if err := c.LoadResponseFormat(path); err != nil {
		t.Fatalf("LoadResponseFormat: %v", err)
	}
	b, err := json.Marshal(toGenericResponseFormat(c.ResponseFormat))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	body := string(b)
	if ship, reason := strings.Index(body, `"ship"`), strings.Index(body, `"reason"`); ship < 0 || ship > reason {
		t.Fatalf("schema property order lost, want ship before reason:\n%s", body)
	}
}
