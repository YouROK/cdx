// Package ipc implements a line-delimited JSON bidirectional streaming protocol over a Unix domain socket.
package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
)

// Frame types exchanged between the CLI client and the daemon over a connection.
const (
	FrameRequest  = "request"  // Client -> Daemon: initial command invocation
	FrameEvent    = "event"    // Daemon -> Client: streaming event (token delta, tool status)
	FramePrompt   = "prompt"   // Daemon -> Client: interactive question (e.g. permission prompt)
	FrameAnswer   = "answer"   // Client -> Daemon: user answer to a prompt
	FrameResponse = "response" // Daemon -> Client: final command result or error
)

// Frame is a single JSON message sent over the Unix socket.
type Frame struct {
	Type    string         `json:"type"`
	Method  string         `json:"method,omitempty"`
	Payload map[string]any `json:"payload,omitempty"`
	Data    any            `json:"data,omitempty"`
	Error   string         `json:"error,omitempty"`
}

// Conn wraps a net.Conn with thread-safe JSON line writing and buffered reading.
type Conn struct {
	raw     net.Conn
	scanner *bufio.Scanner
	writeMu sync.Mutex
}

func wrapConn(c net.Conn) *Conn {
	sc := bufio.NewScanner(c)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	return &Conn{
		raw:     c,
		scanner: sc,
	}
}

// Send writes a Frame as a single JSON line.
func (c *Conn) Send(f Frame) error {
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.raw.Write(data)
	return err
}

// Receive reads the next Frame from the connection.
func (c *Conn) Receive() (Frame, error) {
	if !c.scanner.Scan() {
		if err := c.scanner.Err(); err != nil {
			return Frame{}, err
		}
		return Frame{}, errors.New("connection closed")
	}
	var f Frame
	if err := json.Unmarshal(c.scanner.Bytes(), &f); err != nil {
		return Frame{}, fmt.Errorf("decode ipc frame: %w", err)
	}
	return f, nil
}

// Close closes the underlying socket connection.
func (c *Conn) Close() error {
	return c.raw.Close()
}

// ServerStream allows a daemon handler to emit streaming events and ask interactive prompts to the connected client.
type ServerStream struct {
	conn    *Conn
	ctx     context.Context
	answers chan Frame
}

// Emit sends a streaming event to the client.
func (s *ServerStream) Emit(event any) error {
	return s.conn.Send(Frame{
		Type: FrameEvent,
		Data: event,
	})
}

// Prompt sends a question payload to the client and waits for its answer.
func (s *ServerStream) Prompt(question map[string]any) (map[string]any, error) {
	if err := s.conn.Send(Frame{
		Type:    FramePrompt,
		Payload: question,
	}); err != nil {
		return nil, err
	}
	select {
	case reply := <-s.answers:
		if reply.Type != FrameAnswer {
			return nil, fmt.Errorf("expected answer frame, got %q", reply.Type)
		}
		return reply.Payload, nil
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
}

func (s *ServerStream) readLoop(cancel context.CancelFunc) {
	defer cancel()
	for {
		frame, err := s.conn.Receive()
		if err != nil {
			return
		}
		if frame.Type != FrameAnswer {
			continue
		}
		select {
		case s.answers <- frame:
		case <-s.ctx.Done():
			return
		}
	}
}

// Handler processes an incoming IPC method invocation.
type Handler func(ctx context.Context, method string, payload map[string]any, stream *ServerStream) (any, error)

// Server listens on a Unix domain socket and dispatches client requests.
type Server struct {
	socketPath string
	handler    Handler
	listener   net.Listener
	wg         sync.WaitGroup
	closeOnce  sync.Once
	closed     chan struct{}
}

// NewServer creates a Unix socket IPC server.
func NewServer(socketPath string, handler Handler) *Server {
	return &Server{
		socketPath: socketPath,
		handler:    handler,
		closed:     make(chan struct{}),
	}
}

// Start binds the Unix socket and starts accepting client connections in the background.
func (s *Server) Start() error {
	if err := os.MkdirAll(filepath.Dir(s.socketPath), 0o700); err != nil {
		return fmt.Errorf("create socket dir: %w", err)
	}
	_ = os.Remove(s.socketPath)

	ln, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("listen unix %s: %w", s.socketPath, err)
	}
	_ = os.Chmod(s.socketPath, 0o600)
	s.listener = ln

	s.wg.Add(1)
	go s.acceptLoop()
	return nil
}

// Close stops accepting connections and removes the socket file.
func (s *Server) Close() error {
	var err error
	s.closeOnce.Do(func() {
		close(s.closed)
		if s.listener != nil {
			err = s.listener.Close()
		}
		_ = os.Remove(s.socketPath)
		s.wg.Wait()
	})
	return err
}

func (s *Server) acceptLoop() {
	defer s.wg.Done()
	for {
		raw, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.closed:
				return
			default:
				continue
			}
		}
		s.wg.Add(1)
		go func(c net.Conn) {
			defer s.wg.Done()
			s.serveConn(c)
		}(raw)
	}
}

func (s *Server) serveConn(raw net.Conn) {
	defer raw.Close()
	conn := wrapConn(raw)

	reqFrame, err := conn.Receive()
	if err != nil || reqFrame.Type != FrameRequest {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream := &ServerStream{
		conn:    conn,
		ctx:     ctx,
		answers: make(chan Frame, 4),
	}
	go stream.readLoop(cancel)

	res, callErr := s.handler(ctx, reqFrame.Method, reqFrame.Payload, stream)
	resp := Frame{
		Type: FrameResponse,
		Data: res,
	}
	if callErr != nil {
		resp.Error = callErr.Error()
	}
	_ = conn.Send(resp)
}

// ClientCallbacks handles streaming events and interactive prompts on the client side during a Call.
type ClientCallbacks struct {
	OnEvent  func(data any)
	OnPrompt func(question map[string]any) (map[string]any, error)
}

// Call connects to the daemon socket, invokes method with payload, processes streaming events/prompts, and returns the final response data.
func Call(ctx context.Context, socketPath, method string, payload map[string]any, cb ClientCallbacks) (any, error) {
	var d net.Dialer
	raw, err := d.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return nil, err
	}
	defer raw.Close()

	go func() {
		<-ctx.Done()
		_ = raw.Close()
	}()

	conn := wrapConn(raw)
	if err := conn.Send(Frame{
		Type:    FrameRequest,
		Method:  method,
		Payload: payload,
	}); err != nil {
		return nil, err
	}

	for {
		frame, err := conn.Receive()
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, err
		}

		switch frame.Type {
		case FrameEvent:
			if cb.OnEvent != nil {
				cb.OnEvent(frame.Data)
			}

		case FramePrompt:
			ans := map[string]any{"allow": false}
			if cb.OnPrompt != nil {
				if userAns, pErr := cb.OnPrompt(frame.Payload); pErr == nil && userAns != nil {
					ans = userAns
				}
			}
			if err := conn.Send(Frame{
				Type:    FrameAnswer,
				Payload: ans,
			}); err != nil {
				return nil, err
			}

		case FrameResponse:
			if frame.Error != "" {
				return nil, errors.New(frame.Error)
			}
			return frame.Data, nil
		}
	}
}
