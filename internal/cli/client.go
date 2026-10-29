package cli

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/yourok/cdx/internal/config"
	"github.com/yourok/cdx/internal/ipc"
	"github.com/yourok/cdx/internal/version"
)

// Client is a connection to the daemon.
type Client struct {
	conn *ipc.Conn
}

// noSpawnEnv disables daemon autostart in a spawned process.
//
// It is set on the process started as the daemon: if that process ends up
// acting as a client, autostart will not fire and the recursion stops.
const noSpawnEnv = "CDX_NO_AUTOSTART"

// Dial connects to the daemon, starting it when needed.
func Dial(cfg *config.Config) (*Client, error) {
	socket, err := cfg.SocketPath()
	if err != nil {
		return nil, err
	}

	c, err := dialOnce(cfg, socket)
	if err == nil {
		return c, nil
	}

	if !cfg.Daemon.Autostart {
		return nil, fmt.Errorf("daemon is not running and autostart is disabled")
	}
	// A spawned process never starts a daemon: otherwise a mistake in the
	// launch arguments turns into an endless chain of daemons.
	if os.Getenv(noSpawnEnv) != "" {
		return nil, fmt.Errorf("daemon is not running and autostart is disabled in this process")
	}

	if err := spawnDaemon(cfg); err != nil {
		return nil, err
	}

	timeout := time.Duration(cfg.Daemon.StartTimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c, err = dialOnce(cfg, socket)
		if err == nil {
			return c, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil, fmt.Errorf("daemon did not start within %s", timeout)
}

// dialOnce tries to connect and perform the handshake.
func dialOnce(cfg *config.Config, socket string) (*Client, error) {
	raw, err := net.DialTimeout("unix", socket, time.Second)
	if err != nil {
		return nil, err
	}

	conn := ipc.NewConn(raw, cfg.Daemon.MaxFrameBytes)
	hello := &ipc.Frame{
		Type: ipc.TypeHello,
		Payload: ipc.HelloPayload{
			Proto:  version.Protocol,
			Client: ipc.Endpoint{Version: version.Version, PID: os.Getpid()},
		},
	}
	if err := conn.Write(hello); err != nil {
		_ = conn.Close()
		return nil, err
	}

	resp, err := conn.Read()
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if resp.Type == ipc.TypeHelloEr {
		_ = conn.Close()
		return nil, fmt.Errorf("daemon rejected handshake: %s", resp.Error.Message)
	}
	if resp.Type != ipc.TypeHelloOK {
		_ = conn.Close()
		return nil, fmt.Errorf("unexpected handshake answer %q", resp.Type)
	}
	return &Client{conn: conn}, nil
}

// spawnDaemon starts the daemon in the background under a lock file.
func spawnDaemon(cfg *config.Config) error {
	lockPath, err := cfg.LockPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return fmt.Errorf("create lock dir: %w", err)
	}

	lock, err := acquireLock(lockPath)
	if err != nil {
		// Another client is already starting the daemon, so wait for it.
		return nil
	}
	defer releaseLock(lock, lockPath)

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}

	// The hidden command: the client passes the config path through the
	// environment. The form must match the current argument scheme, since
	// a bare "daemon serve" would parse as a model request, and such a
	// request starts a daemon again, yielding an endless launch.
	args := []string{"--daemon", "serve"}

	cmd := exec.Command(exe, args...)
	cmd.Env = append(os.Environ(), noSpawnEnv+"=1")
	// The daemon must not inherit the client's streams, else the pipe
	// never closes.
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}
	// Release the process: it lives on its own.
	return cmd.Process.Release()
}

// acquireLock takes an exclusive lock.
func acquireLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

// releaseLock drops the lock.
func releaseLock(f *os.File, path string) {
	if f == nil {
		return
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_ = f.Close()
}

// Call performs a request and returns the answer.
func (c *Client) Call(id, method string, payload any) (*ipc.Frame, error) {
	if err := c.conn.Write(&ipc.Frame{Type: ipc.TypeReq, ID: id, Method: method, Payload: payload}); err != nil {
		return nil, err
	}
	return c.conn.Read()
}

// Read reads the next frame.
func (c *Client) Read() (*ipc.Frame, error) {
	return c.conn.Read()
}

// Write sends a frame.
func (c *Client) Write(f *ipc.Frame) error {
	return c.conn.Write(f)
}

// Close closes the connection.
func (c *Client) Close() error {
	return c.conn.Close()
}

// Decode unmarshals a frame payload into a value.
func Decode(f *ipc.Frame, v any) error {
	data, err := ipc.MarshalPayload(f.Payload)
	if err != nil {
		return err
	}
	return ipc.Unmarshal(data, v)
}

// ReadPID reads the daemon pid from the file.
func ReadPID(cfg *config.Config) (int, error) {
	path, err := cfg.PIDPath()
	if err != nil {
		return 0, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

// IsRunning reports whether the daemon answers.
func IsRunning(cfg *config.Config) bool {
	socket, err := cfg.SocketPath()
	if err != nil {
		return false
	}
	c, err := dialOnce(cfg, socket)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// ErrNotRunning is returned when the daemon does not answer.
var ErrNotRunning = errors.New("daemon is not running")

// connect connects without starting the daemon.
func connect(cfg *config.Config) (*Client, error) {
	socket, err := cfg.SocketPath()
	if err != nil {
		return nil, err
	}
	return dialOnce(cfg, socket)
}
