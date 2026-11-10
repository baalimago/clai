package runenv

import (
	"strings"
	"testing"
)

// TestSubsystemsSayWhatTheyReport pins that every subsystem is registered
// once, with a one-line purpose the usage can print, and that the type
// registry is built from that same list.
func TestSubsystemsSayWhatTheyReport(t *testing.T) {
	seen := map[string]bool{}
	for _, subsystem := range Subsystems() {
		if subsystem.Name == "" || subsystem.Name != strings.ToLower(subsystem.Name) {
			t.Errorf("subsystem name %q, want a lower-case name", subsystem.Name)
		}
		if seen[subsystem.Name] {
			t.Errorf("subsystem %q is registered twice", subsystem.Name)
		}
		seen[subsystem.Name] = true
		purpose := subsystem.Purpose
		if purpose == "" || strings.ContainsAny(purpose, "\n%") || len(purpose) > 70 {
			t.Errorf("subsystem %q purpose %q, want one short line without a %% sign", subsystem.Name, purpose)
		}
	}
	for _, name := range []string{SubsystemClai, SubsystemMCP, SubsystemTextQuerier, SubsystemVendorOpenAI, "vendor.xai"} {
		if !seen[name] {
			t.Errorf("subsystem %q is not registered", name)
		}
	}
	typed := map[string]bool{}
	for _, diagnosticType := range Types() {
		typed[diagnosticType.Subsystem] = true
	}
	for name := range seen {
		if !typed[name] {
			t.Errorf("subsystem %q has no diagnostic type", name)
		}
	}
	for name := range typed {
		if !seen[name] {
			t.Errorf("diagnostic types name the unregistered subsystem %q", name)
		}
	}
}
