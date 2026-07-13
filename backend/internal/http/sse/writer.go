// Package sse is a tiny SSE frame writer shared by the search-stream and
// (future) object-Q&A endpoints.
package sse

import (
	"bufio"
	"encoding/json"
	"fmt"
)

type Writer struct {
	w *bufio.Writer
}

func New(w *bufio.Writer) *Writer {
	return &Writer{w: w}
}

// WriteEvent writes one SSE frame and flushes immediately. A non-nil error
// means the client is gone — callers should stop producing further events.
func (s *Writer) WriteEvent(event string, data any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, b); err != nil {
		return err
	}
	return s.w.Flush()
}
