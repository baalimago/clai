package chat

import "testing"

func TestLabelFor(t *testing.T) {
	cases := []struct {
		name, title, first, want string
	}{
		{"title wins", "Fix auth", "please fix the auth bug", "Fix auth"},
		{"empty title falls back to the first message", "", "please fix the auth bug", "please fix the auth bug"},
		{"both empty", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := labelFor(tc.title, tc.first); got != tc.want {
				t.Fatalf("labelFor(%q, %q) = %q, want %q", tc.title, tc.first, got, tc.want)
			}
		})
	}
}
