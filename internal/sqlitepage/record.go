package sqlitepage

import (
	"encoding/binary"
	"fmt"
	"math"
	"unicode/utf16"
)

var intSizes = [...]int{0, 1, 2, 3, 4, 6, 8}

// decodeRecord splits one b-tree payload into column values.
func (db *DB) decodeRecord(payload []byte) ([]Value, error) {
	hdrLen, n, err := varint(payload)
	if err != nil {
		return nil, err
	}
	if hdrLen < int64(n) || int64(len(payload)) < hdrLen {
		return nil, fmt.Errorf("%w: record header length %d", ErrCorrupt, hdrLen)
	}
	serial := make([]int64, 0, 8)
	for off := n; off < int(hdrLen); {
		st, k, err := varint(payload[off:])
		if err != nil {
			return nil, err
		}
		serial = append(serial, st)
		off += k
	}
	body := payload[hdrLen:]
	vals := make([]Value, 0, len(serial))
	for _, st := range serial {
		v, used, err := db.decodeValue(st, body)
		if err != nil {
			return nil, err
		}
		vals = append(vals, v)
		body = body[used:]
	}
	return vals, nil
}

func (db *DB) decodeValue(st int64, body []byte) (Value, int, error) {
	switch {
	case st == 0:
		return Value{Kind: Null}, 0, nil
	case st >= 1 && st <= 6:
		size := intSizes[st]
		if len(body) < size {
			return Value{}, 0, fmt.Errorf("%w: short integer body", ErrCorrupt)
		}
		var v int64
		for _, c := range body[:size] {
			v = v<<8 | int64(c)
		}
		shift := uint(64 - 8*size)
		return Value{Kind: Integer, Int: v << shift >> shift}, size, nil
	case st == 7:
		if len(body) < 8 {
			return Value{}, 0, fmt.Errorf("%w: short real body", ErrCorrupt)
		}
		return Value{Kind: Real, Real: math.Float64frombits(binary.BigEndian.Uint64(body[:8]))}, 8, nil
	case st == 8:
		return Value{Kind: Integer, Int: 0}, 0, nil
	case st == 9:
		return Value{Kind: Integer, Int: 1}, 0, nil
	case st == 10 || st == 11:
		return Value{}, 0, fmt.Errorf("%w: reserved serial type %d", ErrCorrupt, st)
	}
	if st >= 12 && st%2 == 0 {
		size := int((st - 12) / 2)
		if len(body) < size {
			return Value{}, 0, fmt.Errorf("%w: short blob body", ErrCorrupt)
		}
		return Value{Kind: Blob, Blob: body[:size]}, size, nil
	}
	size := int((st - 13) / 2)
	if len(body) < size {
		return Value{}, 0, fmt.Errorf("%w: short text body", ErrCorrupt)
	}
	return Value{Kind: Text, Text: db.decodeText(body[:size])}, size, nil
}

func (db *DB) decodeText(b []byte) string {
	switch db.encoding {
	case 2, 3:
		if len(b)%2 != 0 {
			return string(b)
		}
		u := make([]uint16, len(b)/2)
		for i := range u {
			if db.encoding == 2 {
				u[i] = binary.LittleEndian.Uint16(b[2*i:])
			} else {
				u[i] = binary.BigEndian.Uint16(b[2*i:])
			}
		}
		return string(utf16.Decode(u))
	default:
		return string(b)
	}
}
