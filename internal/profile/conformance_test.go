package profile

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
)

// conformanceFixture is the corpus the Hub's conformance.test.ts reads too:
// bodies and the exact problems the Publish route must answer for each, as
// `<member>: <reason>`. Its copy in clai-cloud is byte-identical; phase 5's
// cross-repository step compares the two.
type conformanceFixture struct {
	Bodies []struct {
		Name     string   `json:"name"`
		Body     string   `json:"body"`
		Problems []string `json:"problems"`
	} `json:"bodies"`
}

// TestConformanceFixture pins this checker's Publish answers to the fixture
// the Hub's TypeScript evaluator is held to.
func TestConformanceFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/conformance.json")
	if err != nil {
		t.Fatalf("read the conformance fixture: %v", err)
	}
	var fixture conformanceFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("decode the conformance fixture: %v", err)
	}
	if len(fixture.Bodies) == 0 {
		t.Fatal("the conformance fixture holds no body")
	}
	for _, row := range fixture.Bodies {
		t.Run(row.Name, func(t *testing.T) {
			findings, err := Validate([]byte(row.Body), Publish, Env{})
			if err != nil {
				t.Fatalf("Validate(%q) = %v, want no check error", row.Name, err)
			}
			if got := publishProblems(findings); !slices.Equal(got, row.Problems) {
				t.Errorf("Validate(%q) Publish problems = %q, want %q", row.Name, got, row.Problems)
			}
		})
	}
}

// publishProblems is the fixture's line form: the reasons of the errors the
// Publish route refuses, each after the member it names.
func publishProblems(findings Findings) []string {
	out := make([]string, 0, len(findings))
	for _, finding := range findings {
		if finding.Severity != Error || !finding.appliesTo(Publish) {
			continue
		}
		out = append(out, finding.Member+": "+finding.Reason)
	}
	return out
}
