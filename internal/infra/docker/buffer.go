package docker

import "strconv"

// boundedBuffer keeps at most limit bytes and counts what overflowed, so
// hook or worker output cannot grow unbounded in memory.
type boundedBuffer struct {
	limit   int
	buf     []byte
	dropped int
}

func newBoundedBuffer(limit int) *boundedBuffer { return &boundedBuffer{limit: limit} }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	room := b.limit - len(b.buf)
	if room > 0 {
		if len(p) < room {
			room = len(p)
		}
		b.buf = append(b.buf, p[:room]...)
		b.dropped += len(p) - room
	} else {
		b.dropped += len(p)
	}
	return len(p), nil
}

func (b *boundedBuffer) String() string {
	if b.dropped > 0 {
		return string(b.buf) + "\n... (" + strconv.Itoa(b.dropped) + " bytes truncated)"
	}
	return string(b.buf)
}
