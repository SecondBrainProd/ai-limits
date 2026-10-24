package agent

import (
	"bytes"
	"io"
	"sync"
)

// timestampLen is the length of the "2006-01-02T15:04:05Z " prefix of every log line.
const timestampLen = len("2006-01-02T15:04:05Z ")

// NewChangeLog wraps w so that a line is written only when its message (without
// the timestamp) differs from the previous one: a helper that stays logged out or
// unpaired for days writes one line, not one every 3 minutes.
func NewChangeLog(w io.Writer) io.Writer { return &changeLog{w: w} }

type changeLog struct {
	mu   sync.Mutex
	w    io.Writer
	last []byte
}

func (c *changeLog) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	msg := p
	if len(msg) > timestampLen {
		msg = msg[timestampLen:]
	}
	if c.last != nil && bytes.Equal(msg, c.last) {
		return len(p), nil
	}
	c.last = append([]byte(nil), msg...)
	if _, err := c.w.Write(p); err != nil {
		return 0, err
	}
	return len(p), nil
}
