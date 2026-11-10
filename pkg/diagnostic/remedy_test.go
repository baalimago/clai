package diagnostic_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/tools/mcp/serverconfig"
	"github.com/baalimago/clai/pkg/diagnostic"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type fakeRemedier struct {
	remedies []diagnostic.Remedy
}

func (e *fakeRemedier) Error() string { return "fake failure" }

func (e *fakeRemedier) Remedies() []diagnostic.Remedy {
	return append([]diagnostic.Remedy(nil), e.remedies...)
}

func TestRemedierContract(t *testing.T) {
	t.Parallel()

	wantGuidance := []diagnostic.Remedy{{
		Instruction: "Set API_TOKEN in the environment.",
		Command:     []string{"export", "API_TOKEN=<value>"},
	}}
	tests := []struct {
		name string
		err  error
		want []diagnostic.Remedy
	}{
		{name: "fake guidance", err: &fakeRemedier{remedies: wantGuidance}, want: wantGuidance},
		{name: "fake no known guidance", err: &fakeRemedier{}},
		{name: "MCP missing environment", err: &serverconfig.MissingEnvError{ServerName: "billing", Missing: []string{"API_TOKEN"}}, want: []diagnostic.Remedy{{Instruction: `Set API_TOKEN in the environment before starting MCP server "billing".`}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			originalText := test.err.Error()
			wrapped := errors.Join(errors.New("other cause"), test.err)
			var remedier diagnostic.Remedier
			if !errors.As(wrapped, &remedier) {
				t.Fatalf("Remedies.1: errors.As(%T) did not find diagnostic.Remedier", test.err)
			}
			if got := remedier.Remedies(); !slices.EqualFunc(got, test.want, equalRemedy) {
				t.Fatalf("Remedies.1: Remedies() = %#v, want %#v", got, test.want)
			}
			if test.err.Error() != originalText {
				t.Fatalf("Remedies.1: inspecting remedies changed Error() from %q to %q", originalText, test.err.Error())
			}
		})
	}

	plain := errors.New("failure without known guidance")
	var remedier diagnostic.Remedier
	if errors.As(plain, &remedier) {
		t.Fatalf("Remedies.1: plain error unexpectedly implements Remedier")
	}
	if plain.Error() != "failure without known guidance" {
		t.Fatalf("Remedies.1: plain error became unusable: %q", plain)
	}
}

func TestMissingEnvRemedy(t *testing.T) {
	t.Parallel()

	const credential = "credential-value-must-not-appear"
	server := pub_models.McpServer{
		Name:    "billing",
		Command: "billing-mcp",
		Auth:    &pub_models.McpServerAuth{RequiredEnv: []string{"BILLING_TOKEN", "AUDIT_TOKEN"}},
	}
	err := serverconfig.CheckRequiredEnv(server, func(name string) string {
		if name == "AUDIT_TOKEN" {
			return credential
		}
		return ""
	})
	if err == nil {
		t.Fatal("Criterion 3: CheckRequiredEnv() = nil, want a missing-environment error")
	}

	var remedier diagnostic.Remedier
	if !errors.As(err, &remedier) {
		t.Fatalf("Criterion 3: errors.As(%T) did not find diagnostic.Remedier", err)
	}
	remedies := remedier.Remedies()
	if len(remedies) != 1 {
		t.Fatalf("Criterion 3: Remedies() = %#v, want one action", remedies)
	}
	display := remedies[0].Instruction + " " + strings.Join(remedies[0].Command, " ")
	for _, want := range []string{"BILLING_TOKEN", "Set", "environment"} {
		if !strings.Contains(display, want) {
			t.Errorf("Criterion 3: remedy %q does not name action %q", display, want)
		}
	}
	for _, forbidden := range []string{credential, "AUDIT_TOKEN"} {
		if strings.Contains(display, forbidden) {
			t.Errorf("Criterion 3: remedy %q exposes %q", display, forbidden)
		}
	}
}

func equalRemedy(left, right diagnostic.Remedy) bool {
	return left.Instruction == right.Instruction && slices.Equal(left.Command, right.Command)
}
