package handlers

import (
	"bufio"
	"bytes"
	"io"
	"net/http"
	"time"
)

// scanLinesOrCR splits on \n and on \r, podman pull progress only ever writes \r and became one huge line
func scanLinesOrCR(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// newCommandScanner reads command output line by line without the 64KB line limit that stopped the stream
func newCommandScanner(r io.Reader) *bufio.Scanner {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	s.Split(scanLinesOrCR)
	return s
}

// longStream lifts the server's 30s WriteTimeout for a response that streams a long command, it cut account creation mid-way
func longStream(w http.ResponseWriter, d time.Duration) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(d))
}
