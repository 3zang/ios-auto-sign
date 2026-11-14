package web

import (
	"io"
	"strings"
	"sync"
	"time"
)

// LogEntry is one line captured from the process log output.
type LogEntry struct {
	Seq     int64  `json:"seq"`
	Time    string `json:"time"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

// LogBuffer is a concurrency-safe ring buffer that also mirrors every write to
// an underlying sink (typically os.Stderr) so existing log routing is kept. It
// is wired as the process log output in New, which lets the console expose
// recent server activity without depending on where stderr is redirected.
type LogBuffer struct {
	mu      sync.Mutex
	buf     []LogEntry
	seq     int64
	max     int
	sink    io.Writer
	pending string
}

// NewLogBuffer creates a ring buffer that keeps at most max entries and, if
// sink is non-nil, forwards every byte to it as well.
func NewLogBuffer(max int, sink io.Writer) *LogBuffer {
	if max < 1 {
		max = 1000
	}
	return &LogBuffer{buf: make([]LogEntry, 0, max), max: max, sink: sink}
}

func levelOf(msg string) string {
	upper := strings.ToUpper(msg)
	switch {
	case strings.Contains(upper, "FATAL"), strings.Contains(upper, "PANIC"):
		return "error"
	case strings.Contains(upper, "ERROR"), strings.Contains(upper, "ERR "),
		strings.Contains(upper, "FAILED"), strings.Contains(upper, "FAIL "),
		strings.Contains(upper, "失败"), strings.Contains(upper, "错误"):
		return "error"
	case strings.Contains(upper, "WARN"), strings.Contains(upper, "警告"):
		return "warn"
	default:
		return "info"
	}
}

// Write satisfies io.Writer. Multi-line writes are split into individual
// entries; a trailing partial line is buffered until the next write.
func (b *LogBuffer) Write(p []byte) (int, error) {
	if b.sink != nil {
		b.sink.Write(p)
	}
	b.mu.Lock()
	data := b.pending + string(p)
	lines := strings.Split(data, "\n")
	b.pending = lines[len(lines)-1]
	for _, line := range lines[:len(lines)-1] {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		b.seq++
		b.buf = append(b.buf, LogEntry{
			Seq:     b.seq,
			Time:    time.Now().Format("2006-01-02 15:04:05"),
			Level:   levelOf(line),
			Message: line,
		})
		if len(b.buf) > b.max {
			b.buf = b.buf[len(b.buf)-b.max:]
		}
	}
	b.mu.Unlock()
	return len(p), nil
}

// Seq returns the highest sequence number written so far.
func (b *LogBuffer) Seq() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.seq
}

// Recent returns buffered entries, optionally only those after since and capped
// to the most recent limit entries.
func (b *LogBuffer) Recent(limit int, since int64) []LogEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := []LogEntry{}
	if since > 0 {
		for _, e := range b.buf {
			if e.Seq > since {
				out = append(out, e)
			}
		}
	} else {
		out = append(out, b.buf...)
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}
