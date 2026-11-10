package mcp

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/yourok/cdx/internal/logx"
)

// stdioTransport runs a server as a subprocess and exchanges newline-delimited frames.
type stdioTransport struct {
	cfg        Config
	log        *logx.Scope
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	writeMu    sync.Mutex
	handler    func([]byte)
	readers    sync.WaitGroup
	closed     chan struct{}
	closedOnce sync.Once
	closeOnce  sync.Once
	closing    atomic.Bool

	diagMu  sync.Mutex
	diag    []string
	exitErr error
	exited  chan struct{}
}

// newStdioTransport creates a transport for a command from the configuration.
func newStdioTransport(cfg Config) *stdioTransport {
	return &stdioTransport{
		cfg:    cfg,
		log:    cfg.logger().Tagged("MCP:" + cfg.ID),
		closed: make(chan struct{}),
		exited: make(chan struct{}),
	}
}

// Start launches the server and begins reading its output.
func (t *stdioTransport) Start(ctx context.Context, handler func([]byte)) error {
	t.handler = handler
	cmd := exec.Command(t.cfg.Command, t.cfg.Args...)
	if t.cfg.Dir != "" {
		cmd.Dir = t.cfg.Dir
	}
	cmd.Env = mergedEnv(t.cfg.Env, t.cfg.Dir)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 2 * time.Second

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("mcp: open server stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("mcp: open server stderr: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("mcp: open server stdin: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("mcp: start %q: %w", t.cfg.Command, err)
	}

	t.cmd = cmd
	t.stdin = stdin
	t.log.Debug("Started server %q in %s", t.cfg.Command, t.cfg.Dir)

	t.readers.Add(2)
	go func() {
		defer t.readers.Done()
		t.readLoop(stdout)
	}()
	go func() {
		defer t.readers.Done()
		t.stderrLoop(stderr)
	}()
	go t.wait()
	return nil
}

// readLoop delivers every stdout line to the client.
func (t *stdioTransport) readLoop(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxMessageSize)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		frame := make([]byte, len(line))
		copy(frame, line)
		t.handler(frame)
	}
	if err := scanner.Err(); err != nil && !t.closing.Load() {
		t.log.Debug("Server stdout ended: %v", err)
	}
	t.markClosed()
}

// stderrLoop logs server diagnostics without parsing them and keeps the last lines.
func (t *stdioTransport) stderrLoop(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		t.log.Debug("Server stderr: %s", line)
		t.diagMu.Lock()
		t.diag = append(t.diag, line)
		if len(t.diag) > 3 {
			t.diag = t.diag[len(t.diag)-3:]
		}
		t.diagMu.Unlock()
	}
}

// wait reaps the process after both pipes reach EOF.
func (t *stdioTransport) wait() {
	t.readers.Wait()
	err := t.cmd.Wait()
	t.diagMu.Lock()
	t.exitErr = err
	t.diagMu.Unlock()
	close(t.exited)
	switch {
	case t.closing.Load():
		t.log.Debug("Server stopped")
	case err != nil:
		t.log.Warn("Server exited: %v", err)
	default:
		t.log.Info("Server exited")
	}
	t.markClosed()
}

// Send writes one frame to the server stdin.
func (t *stdioTransport) Send(ctx context.Context, frame []byte, _ map[string]string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-t.closed:
		return ErrClosed
	default:
	}

	payload := make([]byte, 0, len(frame)+1)
	payload = append(payload, frame...)
	payload = append(payload, '\n')

	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	if t.stdin == nil {
		return ErrClosed
	}
	if _, err := t.stdin.Write(payload); err != nil {
		return fmt.Errorf("mcp: write to server: %w", err)
	}
	return nil
}

// Closed reports that the server output has ended.
func (t *stdioTransport) Closed() <-chan struct{} {
	return t.closed
}

// Failure waits briefly for the process to be reaped and returns the exit error
// with the last stderr lines of the server.
func (t *stdioTransport) Failure() (error, []string) {
	select {
	case <-t.exited:
	case <-time.After(500 * time.Millisecond):
	}
	t.diagMu.Lock()
	defer t.diagMu.Unlock()
	lines := append([]string(nil), t.diag...)
	return t.exitErr, lines
}

// Close stops the process group and releases the pipes.
func (t *stdioTransport) Close() error {
	t.closeOnce.Do(func() {
		t.closing.Store(true)
		if t.stdin != nil {
			_ = t.stdin.Close()
		}
		if t.cmd != nil && t.cmd.Process != nil {
			pid := t.cmd.Process.Pid
			_ = syscall.Kill(-pid, syscall.SIGTERM)
			select {
			case <-t.closed:
			case <-time.After(2 * time.Second):
				_ = syscall.Kill(-pid, syscall.SIGKILL)
			}
		}
		t.markClosed()
	})
	return nil
}

// markClosed closes the closed channel exactly once.
func (t *stdioTransport) markClosed() {
	t.closedOnce.Do(func() { close(t.closed) })
}

// mergedEnv inherits the daemon environment, applies the configured values, and sets PWD.
func mergedEnv(extra map[string]string, dir string) []string {
	values := map[string]string{}
	for _, entry := range os.Environ() {
		name, value, found := strings.Cut(entry, "=")
		if found {
			values[name] = value
		}
	}
	for name, value := range extra {
		values[name] = value
	}
	if dir != "" {
		values["PWD"] = dir
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	env := make([]string, 0, len(names))
	for _, name := range names {
		env = append(env, name+"="+values[name])
	}
	return env
}

// logger returns the configured logger or a stderr logger.
func (c Config) logger() *logx.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return logx.New()
}
