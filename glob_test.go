package main

import (
	"fmt"
	"os"
	"testing"
)

func TestParseGlob(t *testing.T) {
	// Setup a mock filesystem using afero
	tmpDir := t.TempDir()
	os.WriteFile(fmt.Sprintf("%v/%v", tmpDir, "test1.txt"), []byte("content1"), 0644)
	os.WriteFile(fmt.Sprintf("%v/%v", tmpDir, "test2.txt"), []byte("content2"), 0644)

	// Test case
	tests := []struct {
		name    string
		glob    string
		want    int // Number of messages expected
		wantErr bool
	}{
		{"two files", fmt.Sprintf("%v/*.txt", tmpDir), 2, false},
		{"no match", "*.log", 0, false},
		{"invalid pattern", "[", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseGlob(tt.glob)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseGlob() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if len(got) != tt.want {
				t.Errorf("parseGlob() got %v messages, want %v", len(got), tt.want)
			}
		})
	}
}
