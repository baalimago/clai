package sqlitepage

import (
	"encoding/binary"
	"fmt"
)

type pageHeader struct {
	typ      byte
	nCells   int
	hdrOff   int
	ptrStart int
	right    uint32
}

func pageHeaderAt(page []byte, pgno uint32) (pageHeader, error) {
	off := 0
	if pgno == 1 {
		off = headerSize
	}
	if len(page) < off+8 {
		return pageHeader{}, fmt.Errorf("%w: short page %d", ErrCorrupt, pgno)
	}
	h := pageHeader{
		typ:      page[off],
		nCells:   int(binary.BigEndian.Uint16(page[off+3 : off+5])),
		hdrOff:   off,
		ptrStart: off + 8,
	}
	switch h.typ {
	case 0x02, 0x05:
		if len(page) < off+12 {
			return pageHeader{}, fmt.Errorf("%w: short interior page %d", ErrCorrupt, pgno)
		}
		h.right = binary.BigEndian.Uint32(page[off+8 : off+12])
		h.ptrStart = off + 12
	case 0x0a, 0x0d:
	default:
		return pageHeader{}, fmt.Errorf("%w: page type %#x on page %d", ErrUnsupported, h.typ, pgno)
	}
	return h, nil
}

func (h pageHeader) cellOffset(page []byte, i int) (int, error) {
	p := h.ptrStart + 2*i
	if p+2 > len(page) {
		return 0, fmt.Errorf("%w: cell pointer %d outside page", ErrCorrupt, i)
	}
	off := int(binary.BigEndian.Uint16(page[p : p+2]))
	if off < h.hdrOff || off >= len(page) {
		return 0, fmt.Errorf("%w: cell offset %d outside page", ErrCorrupt, off)
	}
	return off, nil
}

// maxPayloadSize bounds a single cell payload to 256 MiB, well below SQLite's
// 1 GiB limit and safe from int overflow on 32-bit platforms.
const maxPayloadSize = 256 << 20

func (db *DB) readTableCell(page []byte, off int) (int64, []byte, error) {
	if off < 0 || off >= len(page) {
		return 0, nil, fmt.Errorf("%w: cell offset %d", ErrCorrupt, off)
	}
	payloadLen, n, err := varint(page[off:])
	if err != nil {
		return 0, nil, err
	}
	off += n
	rowid, n, err := varint(page[off:])
	if err != nil {
		return 0, nil, err
	}
	off += n
	if payloadLen < 0 || payloadLen > maxPayloadSize {
		return 0, nil, fmt.Errorf("%w: invalid payload length %d", ErrCorrupt, payloadLen)
	}
	payload, err := db.readPayload(page, off, int(payloadLen))
	if err != nil {
		return 0, nil, err
	}
	return rowid, payload, nil
}

func (db *DB) readPayload(page []byte, off, size int) ([]byte, error) {
	usable := db.usable
	maxLocal := usable - 35
	local := size
	if size > maxLocal {
		minLocal := (usable-12)*32/255 - 23
		k := minLocal + (size-minLocal)%(usable-4)
		if k <= maxLocal {
			local = k
		} else {
			local = minLocal
		}
	}
	if off < 0 || local < 0 || off+local > len(page) {
		return nil, fmt.Errorf("%w: local payload %d at %d", ErrCorrupt, local, off)
	}
	out := append([]byte(nil), page[off:off+local]...)
	if local == size {
		return out, nil
	}
	if off+local+4 > len(page) {
		return nil, fmt.Errorf("%w: missing overflow pointer", ErrCorrupt)
	}
	next := binary.BigEndian.Uint32(page[off+local : off+local+4])
	seen := map[uint32]bool{}
	for len(out) < size {
		if next == 0 {
			return nil, fmt.Errorf("%w: overflow chain ended early", ErrCorrupt)
		}
		if seen[next] {
			return nil, fmt.Errorf("%w: overflow cycle at page %d", ErrCorrupt, next)
		}
		seen[next] = true
		pg, err := db.readPage(next)
		if err != nil {
			return nil, err
		}
		following := binary.BigEndian.Uint32(pg[0:4])
		chunk := usable - 4
		if want := size - len(out); chunk > want {
			chunk = want
		}
		if 4+chunk > len(pg) {
			return nil, fmt.Errorf("%w: short overflow page %d", ErrCorrupt, next)
		}
		out = append(out, pg[4:4+chunk]...)
		next = following
	}
	return out, nil
}
