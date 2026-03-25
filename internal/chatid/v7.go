package chatid

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// New returns a UUIDv7-formatted chat id.
//
// This local implementation is intentionally small so it can later be replaced
// by the upcoming Go stdlib UUID implementation without changing call sites.
func New() (string, error) {
	var b [16]byte

	tsMillis := uint64(time.Now().UnixMilli())
	b[0] = byte(tsMillis >> 40)
	b[1] = byte(tsMillis >> 32)
	b[2] = byte(tsMillis >> 24)
	b[3] = byte(tsMillis >> 16)
	b[4] = byte(tsMillis >> 8)
	b[5] = byte(tsMillis)

	if _, err := rand.Read(b[6:]); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}

	b[6] = (b[6] & 0x0f) | 0x70
	b[8] = (b[8] & 0x3f) | 0x80

	return formatUUID(b), nil
}

func formatUUID(b [16]byte) string {
	var dst [36]byte
	hex.Encode(dst[0:8], b[0:4])
	dst[8] = '-'
	hex.Encode(dst[9:13], b[4:6])
	dst[13] = '-'
	hex.Encode(dst[14:18], b[6:8])
	dst[18] = '-'
	hex.Encode(dst[19:23], b[8:10])
	dst[23] = '-'
	hex.Encode(dst[24:36], b[10:16])
	return string(dst[:])
}
