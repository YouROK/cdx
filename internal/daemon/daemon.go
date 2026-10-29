// Package daemon runs the daemon and serves client connections.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/yourok/cdx/internal/bus"
	"github.com/yourok/cdx/internal/config"
	"github.com/yourok/cdx/internal/ctxb"
	"github.com/yourok/cdx/internal/ipc"
	"github.com/yourok/cdx/internal/logx"
	"github.com/yourok/cdx/internal/mcp"
	"github.com/yourok/cdx/internal/perm"
	"github.com/yourok/cdx/internal/plugin"
	"github.com/yourok/cdx/internal/registry"
	"github.com/yourok/cdx/internal/secret"
	"github.com/yourok/cdx/internal/sessions"
	"github.com/yourok/cdx/internal/store"
	"github.com/yourok/cdx/internal/version"
)

// State is the daemon state.
type State string

// Possible states.
const (
	StateStarting State = "starting"
	StateRunning  State = "running"
	StateStopping State = "stopping"
	StateStopped  State = "stopped"
	StateError    State = "error"
)

// Daemon is a running daemon.
type Daemon struct {
	cfg   *config.Config
	store *store.Store
	keys  *secret.Keyring
	reg   *registry.Registry
	perms *perm.Manager
	sess  *sessions.Manager
	// summarizer compacts old history; nil disables compaction.
	summarizer *ctxb.Summarizer
	// bus connects plugins, MCP servers and core tools.
	bus     *bus.Bus
	plugins *plugin.Manager
	mcps    *mcp.Manager
	// pluginsDir is the plugin directory, cwd the startup directory.
	pluginsDir string
	cwd        string
	socket     string
	pid        string

	mu      sync.Mutex
	state   State
	started time.Time
	conns   map[*ipc.Conn]struct{}
	turnMu  sync.Mutex
	// turns tracks running turns so they can be cancelled.
	turns *turns
	// turnCWD is the working directory of the running turn.
	//
	// It is the path root for plugins: a plugin works on the user's
	// project, not on its own install directory.
	turnCWD string
	// activeSession is the connection of the running turn.
	//
	// A plugin permission request goes to the client that started the
	// turn, which is the one that can answer it.
	activeSession *session

	ln     net.Listener
	webSrv *http.Server
	webURL string
	// webError keeps why the web interface failed to start.
	webError string
	// cfgPath is kept so the web interface saves edits to the same file.
	cfgPath string
	stopCh  chan struct{}
}

// New creates a daemon.
func New(cfg *config.Config) (*Daemon, error) {
	return NewWithPath(cfg, "")
}

// NewWithPath creates a daemon and remembers the config path.
func NewWithPath(cfg *config.Config, cfgPath string) (*Daemon, error) {
	socket, err := cfg.SocketPath()
	if err != nil {
		return nil, err
	}
	pid, err := cfg.PIDPath()
	if err != nil {
		return nil, err
	}
	dbPath, err := cfg.DBPath()
	if err != nil {
		return nil, err
	}
	dir, err := config.DataDir()
	if err != nil {
		return nil, err
	}

	st, err := store.Open(dbPath)
	if err != nil {
		return nil, err
	}
	keys, err := secret.Open(dir)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	pm, err := perm.New(cfg)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	// The core has no tools: plugins and MCP servers supply them.
	reg := registry.NewRegistry()

	pluginsDir, err := cfg.PluginsPath()
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	cwd, _ := os.Getwd()

	d := &Daemon{
		cfg:        cfg,
		pluginsDir: pluginsDir,
		cwd:        cwd,
		store:      st,
		sess:       sessions.New(st),
		keys:       keys,
		reg:        reg,
		perms:      pm,
		socket:     socket,
		pid:        pid,
		state:      StateStarting,
		conns:      map[*ipc.Conn]struct{}{},
		stopCh:     make(chan struct{}),
		turns:      newTurns(),
		cfgPath:    cfgPath,
	}

	d.initBus()
	return d, nil
}

// Run starts the daemon and blocks until it stops.
func (d *Daemon) Run() error {
	// Legacy records are converted before any model is touched.
	d.migrateLegacyModels()

	if err := os.MkdirAll(filepath.Dir(d.socket), 0o700); err != nil {
		return fmt.Errorf("create socket dir: %w", err)
	}
	// A stale socket from the previous run must go.
	_ = os.Remove(d.socket)

	ln, err := net.Listen("unix", d.socket)
	if err != nil {
		d.setState(StateError)
		return fmt.Errorf("listen on socket: %w", err)
	}
	if err := os.Chmod(d.socket, 0o600); err != nil {
		logx.Warn("[Daemon] Failed to set socket permissions: %v", err)
	}
	d.ln = ln

	if err := os.WriteFile(d.pid, []byte(fmt.Sprintf("%d", os.Getpid())), 0o600); err != nil {
		logx.Warn("[Daemon] Failed to write pid file: %v", err)
	}

	d.started = time.Now()
	d.setState(StateRunning)
	logx.Info("[Daemon] Listening on %s", d.socket)

	// Plugins and MCP start in the background: launching an external
	// server waits on network and processes, while the daemon must
	// accept requests right away.
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logx.Error("[Daemon] Extensions crashed: %v", r)
			}
		}()
		d.startExtensions(context.Background())
	}()

	go d.acceptLoop()

	if d.cfg.Web.Enabled {
		go d.startWeb()
	}

	<-d.stopCh
	return nil
}

// acceptLoop accepts client connections.
func (d *Daemon) acceptLoop() {
	for {
		conn, err := d.ln.Accept()
		if err != nil {
			select {
			case <-d.stopCh:
				return
			default:
			}
			logx.Warn("[Daemon] Accept failed: %v", err)
			continue
		}
		go d.serve(conn)
	}
}

// serve handles one connection.
func (d *Daemon) serve(raw net.Conn) {
	c := ipc.NewConn(raw, d.cfg.Daemon.MaxFrameBytes)
	sess := newSession(c)
	defer func() {
		// A dropped connection cancels the turn that ran over it.
		sess.markDone()
		d.turns.cancelAll(sess)
		d.mu.Lock()
		delete(d.conns, c)
		d.mu.Unlock()
		_ = raw.Close()
	}()

	handshaken := false
	for {
		frame, err := c.Read()
		if err != nil {
			return
		}

		if !handshaken {
			if frame.Type != ipc.TypeHello {
				_ = c.Write(&ipc.Frame{Type: ipc.TypeHelloEr, Error: ipc.NewError(ipc.CodeNotHandshaken, "hello is required")})
				return
			}
			if err := d.handleHello(c, frame); err != nil {
				return
			}
			handshaken = true
			d.mu.Lock()
			d.conns[c] = struct{}{}
			d.mu.Unlock()
			continue
		}

		switch frame.Type {
		case ipc.TypePing:
			_ = c.Write(&ipc.Frame{Type: ipc.TypePong, ID: frame.ID})
		case ipc.TypeCancel:
			// The client cancelled one turn: stop that one, leave the rest.
			if !d.turns.cancel(sess, frame.ID) {
				logx.Debug("[Daemon] Cancel for unknown turn %q", frame.ID)
			}
		case ipc.TypeRes:
			// A reply to a daemon request: hand it to the waiter.
			sess.deliver(frame)
		case ipc.TypeReq:
			go d.handleRequest(sess, frame)
		}
	}
}

// handleHello checks the protocol version.
func (d *Daemon) handleHello(c *ipc.Conn, f *ipc.Frame) error {
	raw, err := jsonBytes(f.Payload)
	if err != nil {
		_ = c.Write(&ipc.Frame{Type: ipc.TypeHelloEr, Error: ipc.NewError(ipc.CodeBadFrame, err.Error())})
		return err
	}

	var hello ipc.HelloPayload
	if err := jsonUnmarshal(raw, &hello); err != nil {
		_ = c.Write(&ipc.Frame{Type: ipc.TypeHelloEr, Error: ipc.NewError(ipc.CodeBadFrame, "invalid hello payload")})
		return err
	}
	if hello.Proto != version.Protocol {
		_ = c.Write(&ipc.Frame{
			Type: ipc.TypeHelloEr,
			Error: ipc.NewError(ipc.CodeProtoMismatch,
				fmt.Sprintf("daemon speaks proto %d, client speaks %d", version.Protocol, hello.Proto)),
		})
		return errors.New("proto mismatch")
	}

	return c.Write(&ipc.Frame{
		Type: ipc.TypeHelloOK,
		Payload: ipc.HelloOKPayload{
			Proto:  version.Protocol,
			Daemon: ipc.Endpoint{Version: version.Version, PID: os.Getpid()},
			State:  string(d.getState()),
			Uptime: int64(time.Since(d.started).Seconds()),
		},
	})
}

// Stop shuts the daemon down.
func (d *Daemon) Stop() {
	d.mu.Lock()
	if d.state == StateStopping || d.state == StateStopped {
		d.mu.Unlock()
		return
	}
	d.state = StateStopping
	d.mu.Unlock()

	logx.Info("[Daemon] Shutdown requested")

	// Extensions stop first: they hold child processes.
	d.stopExtensions()

	if d.webSrv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(d.cfg.Daemon.ShutdownTimeoutMS)*time.Millisecond)
		_ = d.webSrv.Shutdown(ctx)
		cancel()
	}

	if d.ln != nil {
		_ = d.ln.Close()
	}

	d.mu.Lock()
	for c := range d.conns {
		_ = c.Close()
	}
	d.conns = map[*ipc.Conn]struct{}{}
	d.mu.Unlock()

	close(d.stopCh)
	_ = os.Remove(d.socket)
	_ = os.Remove(d.pid)
	_ = d.store.Close()

	d.setState(StateStopped)
	logx.Info("[Daemon] Stopped")
}

// State returns the current state.
func (d *Daemon) State() string { return string(d.getState()) }

func (d *Daemon) getState() State {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state
}

func (d *Daemon) setState(s State) {
	d.mu.Lock()
	d.state = s
	d.mu.Unlock()
}

// Uptime returns how long the daemon has run.
func (d *Daemon) Uptime() int64 { return int64(time.Since(d.started).Seconds()) }
