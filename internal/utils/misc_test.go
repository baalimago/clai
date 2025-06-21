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

func TestDeleteRange(t *testing.T) {
	orig := []int{1, 2, 3, 4, 5, 6, 7, 8, 9}

	t.Run("middle range", func(t *testing.T) {
		tt := make([]int, len(orig))
		copy(tt, orig)
		res, _ := DeleteRange(tt, 2, 5) // Should remove 3,4,5,6 (indices 2-5)
		want := []int{1, 2, 7, 8, 9}
		if !reflect.DeepEqual(res, want) {
			t.Errorf("DeleteRange() = %v, want %v", res, want)
		}
	})
	t.Run("remove first", func(t *testing.T) {
		tt := make([]int, len(orig))
		copy(tt, orig)
		res, _ := DeleteRange(tt, 0, 0)
		want := []int{2, 3, 4, 5, 6, 7, 8, 9}
		if !reflect.DeepEqual(res, want) {
			t.Errorf("DeleteRange(remove first) = %v, want %v", res, want)
		}
	})
	t.Run("remove last", func(t *testing.T) {
		tt := make([]int, len(orig))
		copy(tt, orig)
		res, _ := DeleteRange(tt, len(tt)-1, len(tt)-1)
		want := []int{1, 2, 3, 4, 5, 6, 7, 8}
		if !reflect.DeepEqual(res, want) {
			t.Errorf("DeleteRange(remove last) = %v, want %v", res, want)
		}
	})
	t.Run("remove all", func(t *testing.T) {
		tt := make([]int, len(orig))
		copy(tt, orig)
		res, _ := DeleteRange(tt, 0, len(tt)-1)
		want := []int{}
		if !reflect.DeepEqual(res, want) {
			t.Errorf("DeleteRange(remove all) = %v, want %v", res, want)
		}
	})
}

func TestDeleteRangeInvalidInputs(t *testing.T) {
	orig := []int{1, 2, 3, 4, 5}

	t.Run("invalid range start greater than end", func(t *testing.T) {
		tt := make([]int, len(orig))
		copy(tt, orig)
		_, err := DeleteRange(tt, 3, 2)
		if err == nil {
			t.Errorf("DeleteRange() expected error for start greater than end, got nil")
		}
	})

	t.Run("start index out of bounds", func(t *testing.T) {
		tt := make([]int, len(orig))
		copy(tt, orig)
		_, err := DeleteRange(tt, -1, 2)
		if err == nil {
			t.Errorf("DeleteRange() expected error for start index out of bounds, got nil")
		}
	})

	t.Run("end index out of bounds", func(t *testing.T) {
		tt := make([]int, len(orig))
		copy(tt, orig)
		_, err := DeleteRange(tt, 1, 10)
		if err == nil {
			t.Errorf("DeleteRange() expected error for end index out of bounds, got nil")
		}
	})
}
