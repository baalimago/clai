package text

import (
	"testing"
)

func TestVendorErrorHandler(t *testing.T) {
	for _, tc := range []struct {
		model string
		want  bool
	}{
		{model: "jev-latest", want: true},
		{model: "jev-1.13.0", want: true},
		{model: "gpt-jev-latest"},
		{model: "jev"},
		{model: ""},
	} {
		t.Run(tc.model, func(t *testing.T) {
			got := vendorErrorHandler(tc.model)
			if (got != nil) != tc.want {
				t.Fatalf("vendorErrorHandler(%q) = %T, want handler=%t", tc.model, got, tc.want)
			}
		})
	}
}
