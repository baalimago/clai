package diag_test

import (
	"testing"

	. "github.com/baalimago/clai/internal/diag"
)

func TestSanitizer(t *testing.T) {
	tests := []struct {
		name, given, text, line string
	}{
		{
			name:  "plain text is unchanged",
			given: "server started on port 8080",
			text:  "server started on port 8080",
			line:  "server started on port 8080",
		},
		{
			name:  "a secret shape is redacted in both forms",
			given: "Authorization: Bearer abcdefghijklmnop1234",
			text:  "Authorization: Bearer [REDACTED]",
			line:  "Authorization: Bearer [REDACTED]",
		},
		{
			name:  "controls are escaped in the line form only",
			given: "red \x1b[31mRED\x1b[0m bell \a cr \r nl \n tab \t rtl \u202e bad \xff",
			text:  "red \x1b[31mRED\x1b[0m bell \a cr \r nl \n tab \t rtl \u202e bad \xff",
			line:  `red \x1b[31mRED\x1b[0m bell \x07 cr \x0d nl \x0a tab \x09 rtl \u202e bad ` + "\ufffd",
		},
		{
			name:  "an escaped line is stable under a second pass",
			given: `spin \x1b[31mred\x1b[0m token=[REDACTED]`,
			text:  `spin \x1b[31mred\x1b[0m token=[REDACTED]`,
			line:  `spin \x1b[31mred\x1b[0m token=[REDACTED]`,
		},
	}
	sanitizer := NewSanitizer()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizer.Text(tt.given); got != tt.text {
				t.Errorf("Text = %q, want %q", got, tt.text)
			}
			if got := sanitizer.Line(tt.given); got != tt.line {
				t.Errorf("Line = %q, want %q", got, tt.line)
			}
		})
	}
}
