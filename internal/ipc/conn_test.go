package ipc

import (
	"bytes"
	"io"
	"testing"
)

// pipe is a reader-writer pair for tests.
type pipe struct {
	r *bytes.Buffer
	w *bytes.Buffer
}

func (p *pipe) Read(b []byte) (int, error) {
	if p.r.Len() == 0 {
		return 0, io.EOF
	}
	return p.r.Read(b)
}

func (p *pipe) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p *pipe) Close() error                { return nil }

// TestWriteRead checks a frame round trip.
func TestWriteRead(t *testing.T) {
	p := &pipe{r: &bytes.Buffer{}, w: &bytes.Buffer{}}
	c := NewConn(p, 1024)

	if err := c.Write(&Frame{Type: TypePing, ID: "1"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	p.r = p.w

	f, err := c.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if f.Type != TypePing || f.ID != "1" {
		t.Fatalf("unexpected frame: %+v", f)
	}
}

// TestReadSkipsEmptyLines checks that empty lines are skipped.
func TestReadSkipsEmptyLines(t *testing.T) {
	buf := &bytes.Buffer{}
	buf.WriteString("\n\n{\"type\":\"pong\"}\n")
	p := &pipe{r: buf, w: &bytes.Buffer{}}
	c := NewConn(p, 1024)

	f, err := c.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if f.Type != TypePong {
		t.Fatalf("expected pong, got %q", f.Type)
	}
}

// TestFrameTooLarge checks refusal when the limit is exceeded.
func TestFrameTooLarge(t *testing.T) {
	buf := &bytes.Buffer{}
	buf.WriteString(`{"type":"ping","payload":"` + string(bytes.Repeat([]byte("x"), 200)) + `"}` + "\n")
	p := &pipe{r: buf, w: &bytes.Buffer{}}
	c := NewConn(p, 50)

	if _, err := c.Read(); err != ErrFrameTooLarge {
		t.Fatalf("expected ErrFrameTooLarge, got %v", err)
	}
}

// TestBadJSON checks refusal on invalid JSON.
func TestBadJSON(t *testing.T) {
	buf := &bytes.Buffer{}
	buf.WriteString("{not json}\n")
	p := &pipe{r: buf, w: &bytes.Buffer{}}
	c := NewConn(p, 1024)

	if _, err := c.Read(); err == nil {
		t.Fatal("expected decode error")
	}
}
