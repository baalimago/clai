package capture

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"

	"github.com/baalimago/clai/internal/diag"
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
		return false, diag.Errorf("open wav %q: %w", path, err)
	}
	defer file.Close()

	var header [12]byte
	if _, err := io.ReadFull(file, header[:]); err != nil {
		return false, diag.Errorf("read wav header %q: %w", path, err)
	}
	if string(header[:4]) != "RIFF" || string(header[8:]) != "WAVE" {
		return false, fmt.Errorf("%q is not a RIFF/WAVE file", path)
	}

	threshold := 32768 * math.Pow(10, peakDB/20)
	for {
		var chunk [8]byte
		if _, err := io.ReadFull(file, chunk[:]); err != nil {
			return false, diag.Errorf("wav %q has no data chunk: %w", path, err)
		}
		id := string(chunk[:4])
		size := binary.LittleEndian.Uint32(chunk[4:])
		if id != "data" {
			if _, err := io.CopyN(io.Discard, file, int64(size)+int64(size%2)); err != nil {
				return false, diag.Errorf("skip wav chunk %q of %q: %w", id, path, err)
			}
			continue
		}
		sound, err := samplesAbove(file, size, threshold)
		if err != nil {
			return false, diag.Errorf("scan wav samples of %q: %w", path, err)
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

// wavShape is what settleWAV found on disk: how the header was finished and
// how many bytes of samples the file holds.
type wavShape struct {
	finish    Finish
	dataBytes int64
}

// settleWAV makes the header of the WAV at path agree with the bytes on disk.
// ffmpeg writes 0xFFFFFFFF as both sizes until it finalizes the file, so a
// killed ffmpeg leaves a header no player trusts while the samples are all
// there (measured 2026-11-10 with ffmpeg 8.0.1). A file whose sizes already
// match is not written to.
func settleWAV(path string) (wavShape, error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return wavShape{}, diag.Errorf("open wav %q: %w", path, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return wavShape{}, diag.Errorf("stat wav %q: %w", path, err)
	}

	var header [12]byte
	if _, err := io.ReadFull(file, header[:]); err != nil {
		return wavShape{}, diag.Errorf("read wav header %q: %w", path, err)
	}
	if string(header[:4]) != "RIFF" || string(header[8:]) != "WAVE" {
		return wavShape{}, fmt.Errorf("%q is not a RIFF/WAVE file", path)
	}
	riffSize := int64(binary.LittleEndian.Uint32(header[4:8]))

	offset := int64(len(header))
	blockAlign := int64(1)
	for {
		var chunk [8]byte
		if _, err := file.ReadAt(chunk[:], offset); err != nil {
			return wavShape{}, diag.Errorf("wav %q has no data chunk: %w", path, err)
		}
		id := string(chunk[:4])
		size := int64(binary.LittleEndian.Uint32(chunk[4:]))
		body := offset + int64(len(chunk))
		if id == "fmt " {
			var format [16]byte
			if _, err := file.ReadAt(format[:], body); err != nil {
				return wavShape{}, diag.Errorf("read wav format of %q: %w", path, err)
			}
			blockAlign = max(int64(binary.LittleEndian.Uint16(format[12:14])), 1)
		}
		if id != "data" {
			offset = body + size + size%2
			continue
		}
		onDisk := info.Size() - body
		onDisk -= onDisk % blockAlign
		if size <= onDisk && riffSize == info.Size()-8 {
			return wavShape{finish: FinishFinalized, dataBytes: size}, nil
		}
		if err := writeWAVSizes(file, body, onDisk); err != nil {
			return wavShape{}, diag.Errorf("repair wav header of %q: %w", path, err)
		}
		return wavShape{finish: FinishRepaired, dataBytes: onDisk}, nil
	}
}

// writeWAVSizes ends the file after dataBytes of samples and writes the two
// sizes that follow from it. A recording past the 4 GiB a WAV header can name
// keeps the placeholder, which readers take as "until the end of the file".
func writeWAVSizes(file *os.File, dataStart, dataBytes int64) error {
	end := dataStart + dataBytes
	if err := file.Truncate(end); err != nil {
		return err
	}
	var size [4]byte
	binary.LittleEndian.PutUint32(size[:], uint32(min(end-8, math.MaxUint32)))
	if _, err := file.WriteAt(size[:], 4); err != nil {
		return err
	}
	binary.LittleEndian.PutUint32(size[:], uint32(min(dataBytes, math.MaxUint32)))
	if _, err := file.WriteAt(size[:], dataStart-4); err != nil {
		return err
	}
	return file.Sync()
}
