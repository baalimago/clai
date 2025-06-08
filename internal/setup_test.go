package internal

import "testing"

func TestGetModeFromArgs(t *testing.T) {
	tests := []struct {
		arg  string
		want Mode
	}{
		{"p", PHOTO},
		{"chat", CHAT},
		{"q", QUERY},
		{"glob", GLOB},
		{"re", REPLAY},
		{"cmd", CMD},
		{"setup", SETUP},
		{"version", VERSION},
	}
	for _, tc := range tests {
		got, err := getModeFromArgs(tc.arg)
		if err != nil {
			t.Errorf("unexpected error for %s: %v", tc.arg, err)
		}
		if got != tc.want {
			t.Errorf("mode for %s = %v, want %v", tc.arg, got, tc.want)
		}
	}
	if _, err := getModeFromArgs("unknown"); err == nil {
		t.Error("expected error for unknown command")
	}
}
