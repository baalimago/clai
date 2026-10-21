package sqlitepage

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
)

const (
	walHeaderSize = 32
	walFrameSize  = 24
	walVersion    = 3007000
)

// applyWAL overlays the committed frames of path onto db.
//
// A live writer appends to the log while it is read, so a torn tail, a frame
// with a stale salt or a failing checksum ends the read without an error: the
// log is truncated there and the pages read so far are the committed state. A
// header this reader cannot parse is an error, because the caller cannot tell
// whether the log holds newer pages.
func (db *DB) applyWAL(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	r := bufio.NewReaderSize(f, 64<<10)
	hdr := make([]byte, walHeaderSize)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return fmt.Errorf("%w: short wal header %q: %w", ErrWALPresent, path, err)
	}
	magic := binary.BigEndian.Uint32(hdr[0:4])
	if magic != 0x377f0682 && magic != 0x377f0683 {
		return fmt.Errorf("%w: wal magic %#x in %q", ErrWALPresent, magic, path)
	}
	if v := binary.BigEndian.Uint32(hdr[4:8]); v != walVersion {
		return fmt.Errorf("%w: wal format %d in %q", ErrWALPresent, v, path)
	}
	pageSize := int(binary.BigEndian.Uint32(hdr[8:12]))
	if pageSize == 1 {
		pageSize = 65536
	}
	if pageSize != db.pageSize {
		return fmt.Errorf("%w: wal page size %d, database %d", ErrWALPresent, pageSize, db.pageSize)
	}
	// The low bit of the magic selects the word order of the checksum.
	bigEndian := magic&1 == 1
	salt1 := binary.BigEndian.Uint32(hdr[16:20])
	salt2 := binary.BigEndian.Uint32(hdr[20:24])
	sum1, sum2 := walChecksum(hdr[:24], 0, 0, bigEndian)
	if sum1 != binary.BigEndian.Uint32(hdr[24:28]) || sum2 != binary.BigEndian.Uint32(hdr[28:32]) {
		return fmt.Errorf("%w: wal header checksum in %q", ErrWALPresent, path)
	}

	frame := make([]byte, walFrameSize+pageSize)
	pending := map[uint32][]byte{}
	if db.overlay == nil {
		db.overlay = map[uint32][]byte{}
	}
	for {
		if _, err := io.ReadFull(r, frame[:walFrameSize]); err != nil {
			return walTailError(path, err)
		}
		pgno := binary.BigEndian.Uint32(frame[0:4])
		commitSize := binary.BigEndian.Uint32(frame[4:8])
		if binary.BigEndian.Uint32(frame[8:12]) != salt1 ||
			binary.BigEndian.Uint32(frame[12:16]) != salt2 {
			return nil
		}
		if _, err := io.ReadFull(r, frame[walFrameSize:]); err != nil {
			return walTailError(path, err)
		}
		next1, next2 := walChecksum(frame[:8], sum1, sum2, bigEndian)
		next1, next2 = walChecksum(frame[walFrameSize:], next1, next2, bigEndian)
		if next1 != binary.BigEndian.Uint32(frame[16:20]) || next2 != binary.BigEndian.Uint32(frame[20:24]) {
			return nil
		}
		sum1, sum2 = next1, next2
		if pgno == 0 {
			return nil
		}
		page := make([]byte, pageSize)
		copy(page, frame[walFrameSize:])
		pending[pgno] = page
		if commitSize == 0 {
			continue
		}
		maps.Copy(db.overlay, pending)
		pending = map[uint32][]byte{}
		db.pages = commitSize
	}
}

// walTailError reports a truncated log as the end of the log and anything else
// as the I/O failure it is: a short read is a normal race with a live writer.
func walTailError(path string, err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return nil
	}
	return fmt.Errorf("read %s: %w", path, err)
}

// walChecksum folds data into the cumulative checksum SQLite writes into a
// write-ahead log. data must be a multiple of 8 bytes.
func walChecksum(data []byte, s1, s2 uint32, bigEndian bool) (uint32, uint32) {
	for i := 0; i+8 <= len(data); i += 8 {
		var x0, x1 uint32
		if bigEndian {
			x0 = binary.BigEndian.Uint32(data[i : i+4])
			x1 = binary.BigEndian.Uint32(data[i+4 : i+8])
		} else {
			x0 = binary.LittleEndian.Uint32(data[i : i+4])
			x1 = binary.LittleEndian.Uint32(data[i+4 : i+8])
		}
		s1 += x0 + s2
		s2 += x1 + s1
	}
	return s1, s2
}
