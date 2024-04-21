package tools

import (
	"errors"
	"testing"
)

func Test_tree_validate(t *testing.T) {
	testCases := []struct {
		desc  string
		given Input
		want  error
	}{
		{
			desc: "it should return nil if the UserFunction is valid",
			given: Input{
				"directory": "/home/user",
				"level":     2,
			},
			want: nil,
		},
		{
			desc: "it should return validation error if directory is missing",
			given: Input{
				"level": 2,
			},
			want: NewValidationError([]string{"directory"}),
		},
		{
			desc: "it should return validation error if level is missing",
			given: Input{
				"directory": "some/dir",
			},
			want: NewValidationError([]string{"level"}),
		},
		{
			desc: "it should return validation error if level is not an int",
			given: Input{
				"directory": "some/dir",
			},
			want: NewValidationError([]string{"level"}),
		},
	}
	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			got := FileTree.Validate(tC.given)
			var gotErr ValidationError
			if errors.As(got, &gotErr) {
				if gotErr.Error() != tC.want.Error() {
					t.Fatalf("expected: %v, got: %v", tC.want, got)
				}
			}
		})
	}
}
