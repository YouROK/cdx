package ipc

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// ErrFrameTooLarge is returned when a frame exceeds the limit.
var ErrFrameTooLarge = errors.New("frame too large")

// Conn reads and writes frames as one JSON per line.
type Conn struct {
	rw      io.ReadWriteCloser
	r       *bufio.Reader
	w       *bufio.Writer
	mu      sync.Mutex
	maxSize int
}

// NewConn wraps a stream into a framed connection.
func NewConn(rw io.ReadWriteCloser, maxSize int) *Conn {
	if maxSize <= 0 {
		maxSize = 16 << 20
	}
	return &Conn{
		rw:      rw,
		r:       bufio.NewReaderSize(rw, 64<<10),
		w:       bufio.NewWriterSize(rw, 64<<10),
		maxSize: maxSize,
	}
}

// Read reads a single frame.
func (c *Conn) Read() (*Frame, error) {
	var line []byte
	for {
		chunk, err := c.r.ReadSlice('\n')
		if err == nil {
			line = append(line, chunk...)
			break
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			line = append(line, chunk...)
			if len(line) > c.maxSize {
				return nil, ErrFrameTooLarge
			}
			continue
		}
		if errors.Is(err, io.EOF) && len(line) > 0 {
			line = append(line, chunk...)
			break
		}
		if errors.Is(err, io.EOF) {
			return nil, io.EOF
		}
		return nil, err
	}

	if len(line) > c.maxSize {
		return nil, ErrFrameTooLarge
	}
	line = trimNewline(line)
	if len(line) == 0 {
		return c.Read()
	}

	f := &Frame{}
	if err := json.Unmarshal(line, f); err != nil {
		return nil, fmt.Errorf("decode frame: %w", err)
	}
	return f, nil
}

// Write sends a frame.
func (c *Conn) Write(f *Frame) error {
	data, err := json.Marshal(f)
	if err != nil {
		return fmt.Errorf("encode frame: %w", err)
	}
	data = append(data, '\n')

	c.mu.Lock()
	defer c.mu.Unlock()

	if _, err := c.w.Write(data); err != nil {
		return err
	}
	return c.w.Flush()
}

// Close closes the underlying stream.
func (c *Conn) Close() error {
	return c.rw.Close()
}

// trimNewline strips trailing newlines.
func trimNewline(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}
