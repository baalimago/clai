package utils

import (
	"reflect"
	"testing"
)

func TestGetFirstTokens(t *testing.T) {
	tests := []struct {
		name   string
		prompt []string
		n      int
		want   []string
	}{
		{
			name:   "Empty prompt",
			prompt: []string{},
			n:      5,
			want:   []string{},
		},
		{
			name:   "Prompt with less than n tokens",
			prompt: []string{"Hello", "World"},
			n:      5,
			want:   []string{"Hello", "World"},
		},
		{
			name:   "Prompt with exactly n tokens",
			prompt: []string{"This", "is", "a", "test", "prompt"},
			n:      5,
			want:   []string{"This", "is", "a", "test", "prompt"},
		},
		{
			name:   "Prompt with more than n tokens",
			prompt: []string{"This", "is", "a", "longer", "test", "prompt"},
			n:      4,
			want:   []string{"This", "is", "a", "longer"},
		},
		{
			name:   "Prompt with empty tokens",
			prompt: []string{"", "Hello", "", "World", ""},
			n:      3,
			want:   []string{"Hello", "World"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetFirstTokens(tt.prompt, tt.n)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetFirstTokens() = %v, want %v", got, tt.want)
			}
		})
	}
}
