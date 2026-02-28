package logutil

import (
	"bytes"
	"io"
	"sync"
	"time"
)

// TimestampWriter wraps an io.Writer and prepends an RFC3339 timestamp
// in brackets at the start of each output line.
type TimestampWriter struct {
	w     io.Writer
	mu    sync.Mutex
	atBOL bool // at beginning of line
}

// NewTimestampWriter returns a writer that prepends [RFC3339] timestamps.
func NewTimestampWriter(w io.Writer) *TimestampWriter {
	return &TimestampWriter{w: w, atBOL: true}
}

func (tw *TimestampWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	tw.mu.Lock()
	defer tw.mu.Unlock()

	written := 0
	for len(p) > 0 {
		if tw.atBOL {
			ts := "[" + time.Now().Format(time.RFC3339) + "] "
			if _, err := tw.w.Write([]byte(ts)); err != nil {
				return written, err
			}
			tw.atBOL = false
		}

		// Find next newline.
		idx := bytes.IndexByte(p, '\n')

		if idx >= 0 {
			// Write up to and including the newline.
			n, err := tw.w.Write(p[:idx+1])
			written += n
			if err != nil {
				return written, err
			}
			p = p[idx+1:]
			tw.atBOL = true
		} else {
			// No newline — write remainder, stay mid-line.
			n, err := tw.w.Write(p)
			written += n
			return written, err
		}
	}

	return written, nil
}
