package schemacache

import (
	"testing"

	"github.com/baalimago/clai/internal/testenv"
)

// The identity rows resolve an npx command line, which looks the launcher up
// on PATH, so the binary needs one whatever the host has installed
// (2026-11-11).
func TestMain(m *testing.M) { testenv.MainWith(m, testenv.FakeBinsSetup("npx", "node")) }
