// Package mcp connects external MCP servers and registers their tools.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// MCP transports.
const (
	TransportStdio = "stdio"
	TransportHTTP  = "http"
)

// The protocol version this client announces.
const protocolVersion = "2024-11-05"

// Client talks to one MCP server over stdio.
type Client struct {
	name    string
	command string
	args    []string
	env     map[string]string
	dir     string
	timeout time.Duration

	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	stderr io.ReadCloser

	nextID atomic.Int64
	// writeMu serializes writes so frames never interleave.
	writeMu sync.Mutex
	// pending holds the waiters by request id.
	pendingMu sync.Mutex
	pending   map[int64]chan response
}

type request struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// Tool is a tool offered by an MCP server.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// Options configures a client.
type Options struct {
	Name    string
	Command string
	Args    []string
	Env     map[string]string
	Dir     string
	Timeout time.Duration
}

// NewClient creates a client.
func NewClient(opts Options) *Client {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Client{
		name:    opts.Name,
		command: opts.Command,
		args:    opts.Args,
		env:     opts.Env,
		dir:     opts.Dir,
		timeout: timeout,
		pending: map[int64]chan response{},
	}
}

// Start launches the server process and performs the handshake.
func (c *Client) Start(ctx context.Context) error {
	cmd := exec.Command(c.command, c.args...)
	cmd.Dir = c.dir

	env := os.Environ()
	for k, v := range c.env {
		env = append(env, k+"="+v)
	}
	cmd.Env = env

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start server: %w", err)
	}

	c.mu.Lock()
	c.cmd = cmd
	c.stdin = stdin
	c.stdout = bufio.NewReaderSize(stdout, 64<<10)
	c.stderr = stderr
	c.mu.Unlock()

	go c.readLoop()
	go c.drainStderr()

	if _, err := c.Call(ctx, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "cdx",
			"version": "1",
		},
	}); err != nil {
		c.Stop()
		return fmt.Errorf("initialize: %w", err)
	}

	// The ready notification has no reply in the protocol.
	_ = c.notify(ctx, "notifications/initialized", nil)
	return nil
}

// readLoop reads replies and hands them to the waiters.
func (c *Client) readLoop() {
	c.mu.Lock()
	reader := c.stdout
	c.mu.Unlock()

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return
		}
		line = trimSpace(line)
		if len(line) == 0 {
			continue
		}

		var resp response
		if err := json.Unmarshal(line, &resp); err != nil {
			// Notifications and noise on the stream must not break the client.
			continue
		}

		c.pendingMu.Lock()
		ch, ok := c.pending[resp.ID]
		if ok {
			delete(c.pending, resp.ID)
		}
		c.pendingMu.Unlock()

		if ok {
			ch <- resp
		}
	}
}

// drainStderr reads stderr so the process never blocks on a full pipe.
func (c *Client) drainStderr() {
	c.mu.Lock()
	reader := c.stderr
	c.mu.Unlock()
	if reader == nil {
		return
	}
	_, _ = io.Copy(io.Discard, reader)
}

// Call sends a request and waits for its reply.
func (c *Client) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)

	ch := make(chan response, 1)
	c.pendingMu.Lock()
	c.pending[id] = ch
	c.pendingMu.Unlock()

	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
	}()

	if err := c.send(request{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
		return nil, err
	}

	timeout := c.timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, fmt.Errorf("request %q timed out after %s", method, timeout)
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("server error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	}
}

// notify sends a notification and does not wait for a reply.
func (c *Client) notify(ctx context.Context, method string, params any) error {
	return c.send(request{JSONRPC: "2.0", Method: method, Params: params})
}

// send writes one frame to the server stream.
func (c *Client) send(msg request) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	data = append(data, '\n')

	c.mu.Lock()
	stdin := c.stdin
	c.mu.Unlock()
	if stdin == nil {
		return fmt.Errorf("server is not running")
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	_, err = stdin.Write(data)
	return err
}

// ListTools asks the server for its tools.
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	raw, err := c.Call(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}

	var body struct {
		Tools []Tool `json:"tools"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("decode tools: %w", err)
	}
	return body.Tools, nil
}

// CallTool runs a server tool.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	raw, err := c.Call(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	})
	if err != nil {
		return "", err
	}

	var body struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return "", fmt.Errorf("decode result: %w", err)
	}

	var sb strings.Builder
	for _, item := range body.Content {
		if item.Type == "text" {
			sb.WriteString(item.Text)
			sb.WriteString("\n")
		}
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

// Stop shuts the server down.
func (c *Client) Stop() {
	c.mu.Lock()
	cmd := c.cmd
	stdin := c.stdin
	stderr := c.stderr
	c.cmd = nil
	c.stdin = nil
	c.stderr = nil
	c.mu.Unlock()

	if stdin != nil {
		_ = stdin.Close()
	}
	if stderr != nil {
		_ = stderr.Close()
	}
	if cmd == nil || cmd.Process == nil {
		return
	}

	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		// The server did not exit on its own, so it is killed.
		_ = cmd.Process.Kill()
	}
}

// Name returns the server name.
func (c *Client) Name() string { return c.name }

func trimSpace(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}
