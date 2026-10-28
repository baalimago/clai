package capture

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
)

// wavHasSound reports whether the WAV at path holds one signed 16-bit sample
// above the level peakDB names. The samples decide whether audio was heard:
// silencedetect reports only transitions, so a source that never pauses emits
// no event at all (measured 2026-10-28 with ffmpeg 8.0.1).
//
// The read costs one pass over the file with a constant buffer, and it stops
// at the first sample above the level.
func wavHasSound(path string, peakDB float64) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, fmt.Errorf("open wav %q: %w", path, err)
	}
	defer file.Close()

	var header [12]byte
	if _, err := io.ReadFull(file, header[:]); err != nil {
		return false, fmt.Errorf("read wav header %q: %w", path, err)
	}
	if string(header[:4]) != "RIFF" || string(header[8:]) != "WAVE" {
		return false, fmt.Errorf("%q is not a RIFF/WAVE file", path)
	}

	threshold := 32768 * math.Pow(10, peakDB/20)
	for {
		var chunk [8]byte
		if _, err := io.ReadFull(file, chunk[:]); err != nil {
			return false, fmt.Errorf("wav %q has no data chunk: %w", path, err)
		}
		id := string(chunk[:4])
		size := binary.LittleEndian.Uint32(chunk[4:])
		if id != "data" {
			if _, err := io.CopyN(io.Discard, file, int64(size)+int64(size%2)); err != nil {
				return false, fmt.Errorf("skip wav chunk %q of %q: %w", id, path, err)
			}
			continue
		}
		sound, err := samplesAbove(file, size, threshold)
		if err != nil {
			return false, fmt.Errorf("scan wav samples of %q: %w", path, err)
		}
		return sound, nil
	}
}

// samplesAbove reports whether the next size bytes hold a sample above
// threshold. A size past the end of the stream, as an interrupted write
// leaves, scans the bytes that are there.
func samplesAbove(r io.Reader, size uint32, threshold float64) (bool, error) {
	buffer := make([]byte, 32*1024)
	remaining := int64(size)
	for remaining > 0 {
		want := min(remaining, int64(len(buffer)))
		read, err := io.ReadFull(r, buffer[:want])
		for i := 0; i+1 < read; i += 2 {
			if math.Abs(float64(int16(binary.LittleEndian.Uint16(buffer[i:])))) > threshold {
				return true, nil
			}
		}
		remaining -= int64(read)
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
	}
	return false, nil
}

func hasRIFFHeader(path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()
	var header [4]byte
	if _, err := io.ReadFull(file, header[:]); err != nil {
		return false, err
	}
	return string(header[:]) == "RIFF", nil
}
