package audio

import (
	"bytes"
	"fmt"
)

// SniffLen is the number of leading bytes DetectExtension needs to inspect.
const SniffLen = 12

// DetectExtension identifies the audio container from the file's leading
// bytes and returns its canonical extension. The extension is part of the
// transcription contract: vendors infer the upload format from the multipart
// filename and ffmpeg infers the chunk container from the output pattern.
func DetectExtension(header []byte) (string, error) {
	switch {
	case len(header) >= SniffLen && bytes.HasPrefix(header, []byte("RIFF")) && bytes.Equal(header[8:12], []byte("WAVE")):
		return ".wav", nil
	case len(header) >= 4 && bytes.HasPrefix(header, []byte("fLaC")):
		return ".flac", nil
	case len(header) >= 4 && bytes.HasPrefix(header, []byte("OggS")):
		return ".ogg", nil
	case len(header) >= 3 && bytes.HasPrefix(header, []byte("ID3")):
		return ".mp3", nil
	case len(header) >= 2 && header[0] == 0xFF && header[1]&0xE0 == 0xE0:
		return ".mp3", nil
	case len(header) >= SniffLen && bytes.Equal(header[4:8], []byte("ftyp")):
		if bytes.HasPrefix(header[8:12], []byte("M4A")) {
			return ".m4a", nil
		}
		return ".mp4", nil
	case len(header) >= 4 && bytes.Equal(header[:4], []byte{0x1A, 0x45, 0xDF, 0xA3}):
		return ".webm", nil
	default:
		return "", fmt.Errorf("unrecognized audio format, expected one of: wav, mp3, flac, ogg, m4a, mp4, webm")
	}
}
