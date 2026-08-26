package text

// tailBuffer is a fixed-size circular byte buffer. Its zero value uses the
// reasoning-buffer limit, while tests can select a smaller capacity.
type tailBuffer struct {
	buf      []byte
	start    int
	length   int
	capacity int
}

func (b *tailBuffer) WriteString(content string) {
	capacity := b.capacity
	if capacity == 0 {
		capacity = maxReasoningBuf
	}
	if len(b.buf) == 0 {
		b.buf = make([]byte, capacity)
	}
	if len(content) >= capacity {
		copy(b.buf, content[len(content)-capacity:])
		b.start = 0
		b.length = capacity
		return
	}

	if b.length < capacity {
		available := capacity - b.length
		toAppend := min(available, len(content))
		b.writeAt((b.start+b.length)%capacity, content[:toAppend])
		b.length += toAppend
		content = content[toAppend:]
	}
	if content == "" {
		return
	}

	b.writeAt(b.start, content)
	b.start = (b.start + len(content)) % capacity
}

func (b *tailBuffer) writeAt(offset int, content string) {
	written := copy(b.buf[offset:], content)
	copy(b.buf, content[written:])
}

func (b *tailBuffer) Len() int {
	return b.length
}

func (b *tailBuffer) String() string {
	if b.length == 0 {
		return ""
	}
	if b.start+b.length <= len(b.buf) {
		return string(b.buf[b.start : b.start+b.length])
	}

	content := make([]byte, b.length)
	written := copy(content, b.buf[b.start:])
	copy(content[written:], b.buf[:b.length-written])
	return string(content)
}

func (b *tailBuffer) Reset() {
	b.start = 0
	b.length = 0
}
