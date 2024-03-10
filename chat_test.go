package main

import (
	"reflect"
	"testing"
)

func TestGetFirstTokens(t *testing.T) {
	tests := []struct {
		name     string
		prompt   []string
		n        int
		expected []string
	}{
		{
			name:     "empty prompt",
			prompt:   []string{},
			n:        5,
			expected: []string{},
		},
		{
			name:     "prompt shorter than n",
			prompt:   []string{"hello"},
			n:        5,
			expected: []string{"hello"},
		},
		{
			name:     "prompt exactly n tokens",
			prompt:   []string{"how", "are", "you", "doing", "today"},
			n:        5,
			expected: []string{"how", "are", "you", "doing", "today"},
		},
		{
			name:     "prompt longer than n",
			prompt:   []string{"this", "is", "a", "test", "prompt", "with", "more", "tokens"},
			n:        5,
			expected: []string{"this", "is", "a", "test", "prompt"},
		},
		{
			name:     "prompt with multi-space separation",
			prompt:   []string{"this  is", "a", "test"},
			n:        3,
			expected: []string{"this", "is", "a"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getFirstTokens(tt.prompt, tt.n)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("getFirstTokens() = %v, expected %v", got, tt.expected)
			}
		})
	}
}
