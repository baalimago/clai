package cost

import "testing"

func TestFormatUSD(t *testing.T) {
	testCases := []struct {
		in   float64
		want string
	}{
		{in: 0, want: "$0"},
		{in: 0.001, want: "$0.001"},
		{in: 0.01, want: "$0.01"},
		{in: 1.2, want: "$1.2"},
		{in: 14.53, want: "$14.53"},
	}
	for _, tc := range testCases {
		if got := FormatUSD(tc.in); got != tc.want {
			t.Fatalf("FormatUSD(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
