package audio

import (
	"strings"
	"testing"
)

func TestDetectExtension(t *testing.T) {
	tests := []struct {
		name   string
		header []byte
		want   string
	}{
		{"wav", []byte("RIFF\x24\x08\x00\x00WAVEfmt "), ".wav"},
		{"flac", []byte("fLaC\x00\x00\x00\x22"), ".flac"},
		{"ogg", []byte("OggS\x00\x02\x00\x00"), ".ogg"},
		{"mp3 id3 tag", []byte("ID3\x04\x00\x00\x00\x00\x00\x00"), ".mp3"},
		{"mp3 frame sync", []byte{0xFF, 0xFB, 0x90, 0x00, 0x00, 0x00}, ".mp3"},
		{"m4a", append([]byte{0x00, 0x00, 0x00, 0x20}, []byte("ftypM4A ")...), ".m4a"},
		{"mp4", append([]byte{0x00, 0x00, 0x00, 0x18}, []byte("ftypisom")...), ".mp4"},
		{"webm", []byte{0x1A, 0x45, 0xDF, 0xA3, 0x9F, 0x42, 0x86, 0x81}, ".webm"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DetectExtension(tt.header)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("expected %v, got %v", tt.want, got)
			}
		})
	}
	t.Run("unknown bytes error and list recognized formats", func(t *testing.T) {
		_, err := DetectExtension([]byte("definitely not audio"))
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		for _, format := range []string{"wav", "mp3", "flac", "ogg", "m4a", "mp4", "webm"} {
			if !strings.Contains(err.Error(), format) {
				t.Errorf("expected %v in error, got: %v", format, err)
			}
		}
	})
	t.Run("too-short input errors", func(t *testing.T) {
		if _, err := DetectExtension([]byte("RI")); err == nil {
			t.Fatal("expected error, got nil")
		}
	})
	t.Run("riff without wave errors", func(t *testing.T) {
		if _, err := DetectExtension([]byte("RIFF-fake-not-wave-x")); err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}
