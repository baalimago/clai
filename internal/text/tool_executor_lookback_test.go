package text

import (
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func Test_boolInput(t *testing.T) {
	tests := []struct {
		name   string
		in     pub_models.Input
		dfault bool
		want   bool
	}{
		{name: "nil input keeps default", in: nil, dfault: true, want: true},
		{name: "missing key keeps default", in: pub_models.Input{}, dfault: true, want: true},
		{name: "bool value", in: pub_models.Input{"k": false}, dfault: true, want: false},
		{name: "string true", in: pub_models.Input{"k": " True "}, dfault: false, want: true},
		{name: "string false", in: pub_models.Input{"k": "FALSE"}, dfault: true, want: false},
		{name: "garbage string keeps default", in: pub_models.Input{"k": "yes"}, dfault: true, want: true},
		{name: "wrong type keeps default", in: pub_models.Input{"k": 1}, dfault: false, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := boolInput(tt.in, "k", tt.dfault); got != tt.want {
				t.Errorf("boolInput = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_intInput(t *testing.T) {
	tests := []struct {
		name   string
		in     pub_models.Input
		dfault int
		want   int
	}{
		{name: "nil input keeps default", in: nil, dfault: 7, want: 7},
		{name: "missing key keeps default", in: pub_models.Input{}, dfault: 7, want: 7},
		{name: "int value", in: pub_models.Input{"k": 3}, dfault: 7, want: 3},
		{name: "int64 value", in: pub_models.Input{"k": int64(4)}, dfault: 7, want: 4},
		{name: "json float value", in: pub_models.Input{"k": 5.0}, dfault: 7, want: 5},
		{name: "numeric string", in: pub_models.Input{"k": " 42 "}, dfault: 7, want: 42},
		{name: "garbage string keeps default", in: pub_models.Input{"k": "many"}, dfault: 7, want: 7},
		{name: "wrong type keeps default", in: pub_models.Input{"k": true}, dfault: 7, want: 7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := intInput(tt.in, "k", tt.dfault); got != tt.want {
				t.Errorf("intInput = %d, want %d", got, tt.want)
			}
		})
	}
}
