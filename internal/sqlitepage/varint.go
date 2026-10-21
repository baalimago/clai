package sqlitepage

import "fmt"

// varint decodes a SQLite variable-length integer: big-endian 7-bit groups,
// nine bytes maximum, the ninth contributing all eight bits.
func varint(b []byte) (int64, int, error) {
	var v uint64
	for i := range 8 {
		if i >= len(b) {
			return 0, 0, fmt.Errorf("%w: truncated varint", ErrCorrupt)
		}
		c := b[i]
		if c < 0x80 {
			return int64(v<<7 | uint64(c)), i + 1, nil
		}
		v = v<<7 | uint64(c&0x7f)
	}
	if len(b) < 9 {
		return 0, 0, fmt.Errorf("%w: truncated varint", ErrCorrupt)
	}
	return int64(v<<8 | uint64(b[8])), 9, nil
}
