package remote

import (
	"errors"
	"strings"
	"testing"
)

func TestValidate_acceptsAProfileAndListsKeysClaiIgnores(t *testing.T) {
	body := `{"description":"Run hourly.","notes":"x","model":"gpt-5.2","mcp_servers":{
		"memory":{"command":"npx","args":["-y","@modelcontextprotocol/server-memory@2025.9.25"]},
		"remote":{"url":"https://mcp.example.com"},
		"poly":{"command":"pnpx","envfile":"~/.secrets.env"},
		"abs":{"command":"x","envfile":"/etc/x.env"},
		"var":{"command":"x","envfile":"$HOME/x.env"}}}`
	got, err := Validate("cchub:a1/b", []byte(body))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	// description is the hub's own field: expected, so not listed.
	if len(got.Ignored) != 1 || got.Ignored[0] != "notes" {
		t.Fatalf("Ignored = %v, want [notes]", got.Ignored)
	}
}

func TestValidate_listsEveryProblem(t *testing.T) {
	body := `{"promt":"x","mcp_servers":{"crm":{"envfile":"secrets.env"},"bad":3}}`
	_, err := Validate("https://example.com/ops.json", []byte(body))
	var invalid *InvalidProfileError
	if !errors.As(err, &invalid) {
		t.Fatalf("err = %v, want *InvalidProfileError", err)
	}
	want := []string{
		`"promt": unknown key (did you mean "prompt"?)`,
		`mcp_servers.bad: needs an object`,
		`mcp_servers.crm: needs "command" or "url"`,
		`mcp_servers.crm.envfile: a relative path has no meaning for a remote profile; use ~/ or an absolute path`,
	}
	if strings.Join(invalid.Problems, "|") != strings.Join(want, "|") {
		t.Fatalf("Problems = %q, want %q", invalid.Problems, want)
	}
	if invalid.ExitCode() != ExitRefused || !strings.Contains(err.Error(), "https://example.com/ops.json is not a valid profile") {
		t.Fatalf("Error/ExitCode = %q %d", err, invalid.ExitCode())
	}
}

func TestValidate_refusesWhatIsNotOneObject(t *testing.T) {
	for _, body := range []string{"", "[]", "null", "{", `{"a":1} {"b":2}`, "<html>", strings.Repeat(" ", MaxBodyBytes+1)} {
		if _, err := Validate("t", []byte(body)); err == nil {
			t.Fatalf("Validate(%.20q) = nil, want error", body)
		}
	}
	if _, err := Validate("t", []byte(`{"mcp_servers":[]}`)); err == nil || !strings.Contains(err.Error(), "object of named servers") {
		t.Fatalf("mcp_servers array: err = %v", err)
	}
}

func TestEditDistance(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{{"", "abc", 3}, {"promt", "prompt", 1}, {"modle", "model", 2}, {"same", "same", 0}} {
		if got := editDistance(tc.a, tc.b); got != tc.want {
			t.Fatalf("editDistance(%q,%q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
	if nearestKey("description", ProfileKeys) != "" || nearestKey("Tool", ProfileKeys) != "tools" {
		t.Fatal("nearestKey")
	}
}
