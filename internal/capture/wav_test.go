package capture

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWAVHasSound_ReadsTheSamples(t *testing.T) {
	cases := []struct {
		name    string
		samples []int16
		peakDB  float64
		want    bool
	}{
		{name: "sound above the threshold", samples: []int16{0, 10000}, peakDB: -40, want: true},
		{name: "negative peak counts", samples: []int16{0, -10000}, peakDB: -40, want: true},
		{name: "silence", samples: []int16{0, 0}, peakDB: -40, want: false},
		{name: "sample at the threshold", samples: []int16{327, -327}, peakDB: -40, want: false},
		{name: "sample above the threshold", samples: []int16{328}, peakDB: -40, want: true},
		{name: "no samples", samples: nil, peakDB: -40, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeWAV(t, wavChunk{id: "fmt ", body: pcmFormat()}, wavChunk{id: "data", body: pcm(tc.samples...)})
			got, err := wavHasSound(path, tc.peakDB)
			if err != nil {
				t.Fatalf("wavHasSound: %v", err)
			}
			if got != tc.want {
				t.Fatalf("wavHasSound = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWAVHasSound_SkipsChunksBeforeData(t *testing.T) {
	path := writeWAV(t,
		wavChunk{id: "fmt ", body: pcmFormat()},
		wavChunk{id: "LIST", body: []byte("INFOISFT\x0d\x00\x00\x00Lavf62.3.100\x00\x00")},
		wavChunk{id: "junk", body: []byte("odd")},
		wavChunk{id: "data", body: pcm(10000)},
	)
	got, err := wavHasSound(path, -40)
	if err != nil {
		t.Fatalf("wavHasSound: %v", err)
	}
	if !got {
		t.Fatalf("wavHasSound = false, want the sample after the LIST and junk chunks")
	}
}

func TestWAVHasSound_ScansTruncatedData(t *testing.T) {
	body := pcm(10000)
	path := writeWAV(t, wavChunk{id: "fmt ", body: pcmFormat()}, wavChunk{id: "data", body: body})
	// Claim two samples, keep one: an interrupted write.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint32(raw[len(raw)-len(body)-4:], 4)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := wavHasSound(path, -40)
	if err != nil {
		t.Fatalf("wavHasSound: %v", err)
	}
	if !got {
		t.Fatalf("wavHasSound = false, want the one sample that was written")
	}
}

func TestWAVHasSound_Failures(t *testing.T) {
	t.Run("no data chunk", func(t *testing.T) {
		path := writeWAV(t, wavChunk{id: "fmt ", body: pcmFormat()})
		_, err := wavHasSound(path, -40)
		if err == nil || !strings.Contains(err.Error(), "no data chunk") {
			t.Fatalf("error = %v, want a missing data chunk", err)
		}
	})

	t.Run("not a RIFF file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "not.wav")
		if err := os.WriteFile(path, []byte("not a wav file"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := wavHasSound(path, -40)
		if err == nil || !strings.Contains(err.Error(), "not a RIFF/WAVE file") {
			t.Fatalf("error = %v, want a format error", err)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		_, err := wavHasSound(filepath.Join(t.TempDir(), "missing.wav"), -40)
		if err == nil || !strings.Contains(err.Error(), "open wav") {
			t.Fatalf("error = %v, want an open error", err)
		}
	})
}

type wavChunk struct {
	id   string
	body []byte
}

func writeWAV(t *testing.T, chunks ...wavChunk) string {
	t.Helper()
	body := []byte("WAVE")
	for _, chunk := range chunks {
		body = append(body, chunk.id...)
		body = binary.LittleEndian.AppendUint32(body, uint32(len(chunk.body)))
		body = append(body, chunk.body...)
		if len(chunk.body)%2 == 1 {
			body = append(body, 0)
		}
	}
	raw := binary.LittleEndian.AppendUint32([]byte("RIFF"), uint32(len(body)))
	raw = append(raw, body...)
	path := filepath.Join(t.TempDir(), "recording.wav")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func pcmFormat() []byte {
	format := binary.LittleEndian.AppendUint16(nil, 1)
	format = binary.LittleEndian.AppendUint16(format, 1)
	format = binary.LittleEndian.AppendUint32(format, 16000)
	format = binary.LittleEndian.AppendUint32(format, 32000)
	format = binary.LittleEndian.AppendUint16(format, 2)
	return binary.LittleEndian.AppendUint16(format, 16)
}

func pcm(samples ...int16) []byte {
	raw := make([]byte, 0, 2*len(samples))
	for _, sample := range samples {
		raw = binary.LittleEndian.AppendUint16(raw, uint16(sample))
	}
	return raw
}
