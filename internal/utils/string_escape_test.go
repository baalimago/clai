package utils

import "testing"

func TestUnescapeConfigString(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "literal escapes", in: `a\nb\tZ`, want: "a\nb\tZ"},
		{name: "real newlines kept", in: "a\nb", want: "a\nb"},
		{name: "no escapes", in: "plain", want: "plain"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := UnescapeConfigString(tt.in); got != tt.want {
				t.Fatalf("UnescapeConfigString(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestRehydrateEscapedConfigString(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "legacy escaped value is rehydrated",
			in:   `Requirements:\n\t* keep it short`,
			want: "Requirements:\n\t* keep it short",
		},
		{
			name: "canonical multi line value is untouched",
			in:   "line1\nline2\n",
			want: "line1\nline2\n",
		},
		{
			name: "mixed value with real newline is untouched",
			in:   "match with the regex \\n here\nand stay",
			want: "match with the regex \\n here\nand stay",
		},
		{
			name: "mixed value with real tab is untouched",
			in:   "in Go indent with \\t\treally",
			want: "in Go indent with \\t\treally",
		},
		{
			name: "no escapes is untouched",
			in:   "plain single line",
			want: "plain single line",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RehydrateEscapedConfigString(tt.in); got != tt.want {
				t.Fatalf("RehydrateEscapedConfigString(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
