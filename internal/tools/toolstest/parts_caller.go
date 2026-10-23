// Package toolstest holds the PartsCaller contract suite, so that TestPartsCallerContract
// (internal/tools/mcp) and every fake PartsCaller run the same cases.
package toolstest

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// PartsSubject is one PartsCaller under the contract suite.
type PartsSubject struct {
	Caller    pub_models.PartsCaller
	ImageIn   pub_models.Input
	WantParts []pub_models.Part
	FailIn    pub_models.Input
	// FailErr is the error the failing input must wrap; nil when only the message is comparable.
	FailErr error
}

type partsCase struct {
	name   string
	proves string
	run    func(t *testing.T, s PartsSubject)
}

func partsCases() []partsCase {
	return []partsCase{
		{"Parts", "CallParts.1", func(t *testing.T, s PartsSubject) {
			parts, err := s.Caller.CallParts(t.Context(), s.ImageIn)
			if err != nil {
				t.Fatalf("CallParts: %v", err)
			}
			if !reflect.DeepEqual(parts, s.WantParts) {
				t.Fatalf("parts = %#v, want %#v", parts, s.WantParts)
			}
			images := 0
			for i, p := range parts {
				img, ok := p.(pub_models.ImagePart)
				if !ok {
					continue
				}
				images++
				toolOrigin := img.Origin == pub_models.OriginTool || img.Origin == pub_models.OriginGenerated
				if !toolOrigin || (img.Asset == "") == (len(img.Data) == 0) {
					t.Fatalf("parts[%d] = %+v, want Origin tool or generated and exactly one of Asset or Data", i, img)
				}
			}
			if images == 0 {
				t.Fatal("the image-returning input returned no ImagePart")
			}
		}},
		{"StringForm", "CallParts.2", func(t *testing.T, s PartsSubject) {
			parts, err := s.Caller.CallParts(t.Context(), s.ImageIn)
			if err != nil {
				t.Fatalf("CallParts: %v", err)
			}
			described := make([]string, 0, len(parts))
			for _, p := range parts {
				described = append(described, p.Describe())
			}
			got, err := s.Caller.Call(s.ImageIn)
			if err != nil {
				t.Fatalf("Call: %v", err)
			}
			if want := strings.Join(described, "\n"); got != want {
				t.Fatalf("Call = %q, want %q", got, want)
			}
		}},
		{"ErrorPassthrough", "CallParts.3", func(t *testing.T, s PartsSubject) {
			parts, partsErr := s.Caller.CallParts(t.Context(), s.FailIn)
			if partsErr == nil {
				t.Fatalf("CallParts = %#v, want an error", parts)
			}
			out, callErr := s.Caller.Call(s.FailIn)
			if callErr == nil || callErr.Error() != partsErr.Error() {
				t.Fatalf("Call = %q, %v; want the CallParts error %v", out, callErr, partsErr)
			}
			if s.FailErr != nil && (!errors.Is(partsErr, s.FailErr) || !errors.Is(callErr, s.FailErr)) {
				t.Fatalf("errors %v / %v do not wrap %v", partsErr, callErr, s.FailErr)
			}
		}},
	}
}

// RunPartsCallerContract runs every case over s, each as a subtest named for its case.
func RunPartsCallerContract(t *testing.T, s PartsSubject) {
	t.Helper()
	for _, c := range partsCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Logf("proves %s", c.proves)
			c.run(t, s)
		})
	}
}
