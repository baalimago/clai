package mcpauth

import "testing"

func TestBuiltinClients(t *testing.T) {
	if got := builtinClients("", ""); len(got) != 0 {
		t.Errorf("builtinClients without an id = %v, want none", got)
	}
	got := builtinClients("id", "secret")
	if reg := got[GitHubIssuer]; reg.ClientID != "id" || reg.ClientSecret != "secret" {
		t.Errorf("builtinClients[%q] = %+v, want id and secret", GitHubIssuer, reg)
	}
	if BuiltinClients() == nil {
		t.Error("BuiltinClients() = nil, want a map")
	}
}
