package main

import (
	"testing"
)

func TestReturnNonDefault(t *testing.T) {
	tests := []struct {
		name       string
		a          interface{}
		b          interface{}
		defaultVal interface{}
		want       interface{}
		wantErr    bool
	}{
		{
			name:       "Both defaults",
			a:          "default",
			b:          "default",
			defaultVal: "default",
			want:       "default",
			wantErr:    false,
		},
		{
			name:       "A non-default",
			a:          "non-default",
			b:          "default",
			defaultVal: "default",
			want:       "non-default",
			wantErr:    false,
		},
		{
			name:       "B non-default",
			a:          "default",
			b:          "non-default",
			defaultVal: "default",
			want:       "non-default",
			wantErr:    false,
		},
		{
			name:       "Both non-default",
			a:          "non-default-a",
			b:          "non-default-b",
			defaultVal: "default",
			want:       "default",
			wantErr:    true,
		},
		{
			name:       "Both non-default same value",
			a:          "non-default",
			b:          "non-default",
			defaultVal: "default",
			want:       "default",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := returnNonDefault(tt.a, tt.b, tt.defaultVal)
			if (err != nil) != tt.wantErr {
				t.Errorf("returnNonDefault() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("returnNonDefault() = %v, want %v", got, tt.want)
			}
		})
	}
}
