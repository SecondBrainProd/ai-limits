package agent

import (
	"bytes"
	"strings"
	"testing"
)

func TestLogOnlyOnStateChange(t *testing.T) {
	var buf bytes.Buffer
	w := NewChangeLog(&buf)
	for _, line := range []string{
		"2026-10-24T10:00:00Z skipped: token expired\n",
		"2026-10-24T10:03:00Z skipped: token expired\n",
		"2026-10-24T10:06:00Z skipped: token expired\n",
		"2026-10-24T10:09:00Z sent\n",
		"2026-10-24T10:12:00Z sent\n",
		"2026-10-24T10:15:00Z skipped: token expired\n",
	} {
		if n, err := w.Write([]byte(line)); err != nil || n != len(line) {
			t.Fatalf("write: %d %v", n, err)
		}
	}
	got := strings.Count(buf.String(), "\n")
	if got != 3 {
		t.Fatalf("want 3 lines (expired, sent, expired), got %d:\n%s", got, buf.String())
	}
}
