// Package daemon wires the core storage, bus, HTTP router, plugin manager, session orchestrator, and IPC server.
package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/yourok/cdx/internal/bus"
	"github.com/yourok/cdx/internal/ipc"
	"github.com/yourok/cdx/internal/logx"
	"github.com/yourok/cdx/internal/mcp"
	"github.com/yourok/cdx/internal/paths"
	"github.com/yourok/cdx/internal/plugin"
	"github.com/yourok/cdx/internal/question"
	"github.com/yourok/cdx/internal/router"
	"github.com/yourok/cdx/internal/session"
	"github.com/yourok/cdx/internal/store"
	"github.com/yourok/cdx/internal/version"
)

// Options configures a Daemon instance.
type Options struct {
	Layout   *paths.Layout
	HTTPAddr string // e.g. "127.0.0.1:8787", or "127.0.0.1:0" for tests, or "" to disable HTTP listener
	Logger   *logx.Logger
}

type streamCtxKey struct{}

// agentRunner runs delegated turns through the session orchestrator.
type agentRunner struct {
	sessions *session.Orchestrator
}

func (r agentRunner) RunChild(ctx context.Context, req plugin.AgentRequest) (plugin.AgentResult, error) {
	result, err := r.sessions.RunChild(ctx, session.TurnRequest{
		Prompt:   req.Prompt,
		Preset:   req.Preset,
		Model:    req.Model,
		MaxSteps: req.MaxSteps,
	})
	if err != nil {
		return plugin.AgentResult{}, err
	}
	return plugin.AgentResult{
		SessionID:     result.Session.ID,
		Content:       result.Content,
		Steps:         result.Steps,
		StoppedReason: result.StoppedReason,
		Usage:         result.Usage,
	}, nil
}

func (r agentRunner) RunChildren(ctx context.Context, reqs []plugin.AgentRequest) ([]plugin.AgentResult, error) {
	results := make([]plugin.AgentResult, len(reqs))
	if len(reqs) == 0 {
		return results, nil
	}

	sem := make(chan struct{}, plugin.MaxAgentTasks)
	var wg sync.WaitGroup
	for i := range reqs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				results[i] = plugin.AgentResult{Error: ctx.Err().Error()}
				return
			}
			result, err := r.RunChild(ctx, reqs[i])
			if err != nil {
				result.Error = err.Error()
			}
			results[i] = result
		}(i)
	}
	wg.Wait()
	return results, nil
}

// clientAsker routes a question to the IPC client of the running turn.
type clientAsker struct{}

func (clientAsker) Ask(ctx context.Context, q question.Question) (question.Answer, error) {
	stream, ok := ctx.Value(streamCtxKey{}).(*ipc.ServerStream)
	if !ok || stream == nil {
		return question.Answer{Cancelled: true, Reason: "no interactive client attached"}, nil
	}
	raw, err := json.Marshal(q)
	if err != nil {
		return question.Answer{}, err
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return question.Answer{}, err
	}
	answer, err := stream.Prompt(payload)
	if err != nil {
		return question.Answer{}, err
	}
	return question.AnswerFromMap(answer), nil
}

// Daemon is the background microkernel process.
type Daemon struct {
	opts     Options
	log      *logx.Scope
	store    *store.Store
	bus      *bus.Bus
	router   *router.Router
	registry *plugin.Registry
	plugins  *plugin.Manager
	sessions *session.Orchestrator
	tasks    *taskManager
	mcp      *mcp.Manager
	ipcSrv   *ipc.Server
	httpSrv  *http.Server
	httpLn   net.Listener

	stopOnce sync.Once
	stopCh   chan struct{}
}

// New initializes all daemon subsystems and loads plugins from the configured plugin directories.
func New(ctx context.Context, opts Options) (*Daemon, error) {
	if opts.Layout == nil {
		layout, err := paths.Resolve()
		if err != nil {
			return nil, err
		}
		opts.Layout = layout
	}
	if err := opts.Layout.EnsureDirs(); err != nil {
		return nil, err
	}
	if opts.Logger == nil {
		opts.Logger = logx.New()
	}

	logScope := opts.Logger.Tagged("Daemon")

	st, err := store.Open(store.Options{
		DBPath:        opts.Layout.DBPath,
		MasterKeyPath: opts.Layout.MasterKeyPath,
	})
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}

	eb := bus.New()
	rt := router.New()
	reg := plugin.NewRegistry()

	sessOrch := session.NewOrchestrator(st, eb, reg, opts.Logger)

	mcpManager := mcp.NewManager()
	tasks := newTaskManager(sessOrch, reg, logScope)
	pm := plugin.NewManager(plugin.RuntimeDependencies{
		Store:     st,
		Bus:       eb,
		Router:    rt,
		Registry:  reg,
		Logger:    opts.Logger,
		Agents:    agentRunner{sessions: sessOrch},
		Tools:     sessOrch,
		Tasks:     tasks,
		MCP:       mcpManager,
		Async:     plugin.NewAsyncLimiter(64),
		AsyncExec: plugin.NewAsyncLimiter(8),
	})

	d := &Daemon{
		opts:     opts,
		log:      logScope,
		store:    st,
		bus:      eb,
		router:   rt,
		registry: reg,
		plugins:  pm,
		sessions: sessOrch,
		tasks:    tasks,
		mcp:      mcpManager,
		stopCh:   make(chan struct{}),
	}

	d.registerCoreRoutes()

	if err := pm.LoadDirs(ctx, opts.Layout.PluginDirs); err != nil {
		_ = st.Close()
		return nil, fmt.Errorf("load plugins: %w", err)
	}

	d.ipcSrv = ipc.NewServer(opts.Layout.SocketPath, d.handleIPC)
	return d, nil
}

// Start begins listening on the IPC socket and optional HTTP router address.
func (d *Daemon) Start() error {
	if err := d.ipcSrv.Start(); err != nil {
		return err
	}

	if d.opts.HTTPAddr != "" {
		ln, err := net.Listen("tcp", d.opts.HTTPAddr)
		if err != nil {
			_ = d.ipcSrv.Close()
			return fmt.Errorf("listen http %s: %w", d.opts.HTTPAddr, err)
		}
		d.httpLn = ln
		d.httpSrv = &http.Server{
			Handler:           d.router,
			ReadHeaderTimeout: 10 * time.Second,
		}
		go func() {
			if err := d.httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
				d.log.Error("HTTP server stopped with error: %v", err)
			}
		}()
		d.log.Info("Started HTTP router on http://%s", ln.Addr().String())
	}

	if d.opts.Layout.PIDPath != "" {
		_ = os.WriteFile(d.opts.Layout.PIDPath, []byte(strconv.Itoa(os.Getpid())), 0o600)
	}

	d.log.Info("Started daemon %s on %s", version.Version, d.opts.Layout.SocketPath)
	return nil
}

// HTTPAddr returns the bound HTTP listener address if running.
func (d *Daemon) HTTPAddr() string {
	if d.httpLn == nil {
		return ""
	}
	return d.httpLn.Addr().String()
}

// Wait blocks until Stop is called.
func (d *Daemon) Wait() {
	<-d.stopCh
}

// Stop shuts down the IPC server, HTTP server, plugins, and BoltDB database.
func (d *Daemon) Stop() {
	d.stopOnce.Do(func() {
		d.log.Info("Stopping daemon")
		if d.httpSrv != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = d.httpSrv.Shutdown(ctx)
			cancel()
		}
		if d.ipcSrv != nil {
			_ = d.ipcSrv.Close()
		}
		if d.tasks != nil {
			d.tasks.CancelAll()
		}
		if d.plugins != nil {
			d.plugins.CloseAll()
		}
		d.mcp.CloseAll()
		if d.store != nil {
			_ = d.store.Close()
		}
		if d.opts.Layout != nil && d.opts.Layout.PIDPath != "" {
			_ = os.Remove(d.opts.Layout.PIDPath)
		}
		close(d.stopCh)
	})
}

func (d *Daemon) registerCoreRoutes() {
	d.router.Handle("core", "GET", "/api/core/status", func(c *gin.Context) {
		models, _ := d.registry.ListModels(c.Request.Context())
		c.JSON(http.StatusOK, gin.H{
			"version":  version.Version,
			"protocol": version.Protocol,
			"pid":      os.Getpid(),
			"plugins":  d.plugins.ListDetailed(),
			"ui":       d.router.ListUI(),
			"tools":    d.registry.ListTools(),
			"models":   models,
		})
	})

	d.router.Handle("core", "GET", "/api/core/plugins", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"plugins": d.plugins.ListDetailed()})
	})

	d.router.Handle("core", "GET", "/api/core/plugins/:id/settings", func(c *gin.Context) {
		rawSchema, values, secrets, ok := d.plugins.SettingsSnapshot(c.Param("id"))
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "plugin has no declarative settings"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"schema":  rawSchema,
			"values":  values,
			"secrets": secrets,
		})
	})

	d.router.Handle("core", "POST", "/api/core/plugins/:id/settings", func(c *gin.Context) {
		var values map[string]any
		if err := c.ShouldBindJSON(&values); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if _, err := d.plugins.SaveSettings(c.Param("id"), values); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		_, current, secrets, ok := d.plugins.SettingsSnapshot(c.Param("id"))
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "plugin has no declarative settings"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"values":  current,
			"secrets": secrets,
		})
	})

	d.router.Handle("core", "POST", "/api/core/plugins/install", func(c *gin.Context) {
		var body struct {
			URL string `json:"url"`
		}
		if err := c.ShouldBindJSON(&body); err != nil || body.URL == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "zip url is required"})
			return
		}
		targetDir := d.plugins.UserPluginDir()
		if targetDir == "" && len(d.opts.Layout.PluginDirs) > 0 {
			targetDir = d.opts.Layout.PluginDirs[0]
		}
		info, err := d.plugins.InstallFromURL(c.Request.Context(), body.URL, targetDir)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"plugin": info})
	})

	d.router.Handle("core", "POST", "/api/core/plugins/:id/enable", func(c *gin.Context) {
		var body struct {
			Enabled bool `json:"enabled"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		info, err := d.plugins.SetEnabled(c.Request.Context(), c.Param("id"), body.Enabled)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"plugin": info})
	})

	d.router.Handle("core", "DELETE", "/api/core/plugins/:id", func(c *gin.Context) {
		if err := d.plugins.RemovePlugin(c.Request.Context(), c.Param("id")); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"deleted": c.Param("id")})
	})

	d.router.Handle("core", "GET", "/api/core/sessions", func(c *gin.Context) {
		list, err := d.sessions.ListSessions()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"sessions":       list,
			"temp_ttl_hours": int(d.sessions.TempTTL().Hours()),
			"live":           d.sessions.LiveSessionIDs(),
		})
	})

	d.router.Handle("core", "POST", "/api/core/sessions/ttl", func(c *gin.Context) {
		var body struct {
			Hours int `json:"hours"`
		}
		if err := c.ShouldBindJSON(&body); err != nil || body.Hours <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "positive hours required"})
			return
		}
		if err := d.sessions.SetTempTTLHours(body.Hours); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"temp_ttl_hours": body.Hours})
	})

	d.router.Handle("core", "GET", "/api/core/agent", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"max_steps":       d.sessions.MaxSteps(),
			"step_limit_mode": d.sessions.StepLimitMode(),
		})
	})

	d.router.Handle("core", "POST", "/api/core/agent", func(c *gin.Context) {
		var body struct {
			MaxSteps      *int    `json:"max_steps"`
			StepLimitMode *string `json:"step_limit_mode"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if body.StepLimitMode != nil {
			if err := d.sessions.SetStepLimitMode(*body.StepLimitMode); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
		}
		if body.MaxSteps != nil {
			if err := d.sessions.SetMaxSteps(*body.MaxSteps); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
		}
		c.JSON(http.StatusOK, gin.H{
			"max_steps":       d.sessions.MaxSteps(),
			"step_limit_mode": d.sessions.StepLimitMode(),
		})
	})

	d.router.Handle("core", "POST", "/api/core/sessions/:id/promote", func(c *gin.Context) {
		sess, err := d.sessions.PromoteSession(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, sess)
	})

	d.router.Handle("core", "POST", "/api/core/sessions/:id/mode", func(c *gin.Context) {
		var body struct {
			Kind string `json:"kind"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		sess, err := d.sessions.SetSessionKind(c.Param("id"), body.Kind)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, sess)
	})

	d.router.Handle("core", "DELETE", "/api/core/sessions/:id", func(c *gin.Context) {
		if err := d.sessions.DeleteSession(c.Param("id")); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"deleted": true})
	})

	d.router.Handle("core", "GET", "/api/core/sessions/:id", func(c *gin.Context) {
		id := c.Param("id")
		sess, found, err := d.sessions.GetSession(id)
		if err != nil || !found {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		msgs, _ := d.sessions.GetMessages(id)
		c.JSON(http.StatusOK, gin.H{
			"session":  sess,
			"messages": msgs,
		})
	})

	d.router.Handle("core", "GET", "/api/core/sessions/:id/live", func(c *gin.Context) {
		id := c.Param("id")
		turn, ok := d.sessions.GetLiveTurn(id)
		if !ok {
			c.JSON(http.StatusOK, gin.H{
				"session_id": id,
				"running":    false,
				"steps":      []session.LiveStep{},
			})
			return
		}
		c.JSON(http.StatusOK, turn)
	})

	d.router.Handle("core", "GET", "/api/core/logs", func(c *gin.Context) {
		logPath := filepath.Join(d.opts.Layout.LogDir, "cdx.log")
		lines := readTailLines(logPath, 500)
		c.JSON(http.StatusOK, gin.H{"lines": lines})
	})
}

func readTailLines(path string, maxLines int) []string {
	f, err := os.Open(path)
	if err != nil {
		return []string{}
	}
	defer f.Close()

	var all []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		all = append(all, sc.Text())
	}
	if len(all) > maxLines {
		all = all[len(all)-maxLines:]
	}
	return all
}

func (d *Daemon) handleIPC(ctx context.Context, method string, payload map[string]any, stream *ipc.ServerStream) (any, error) {
	ctx = context.WithValue(ctx, streamCtxKey{}, stream)
	ctx = question.WithAsker(ctx, clientAsker{})

	switch method {
	case "status":
		models, _ := d.registry.ListModels(ctx)
		return map[string]any{
			"version":        version.Version,
			"protocol":       version.Protocol,
			"http_addr":      d.HTTPAddr(),
			"plugins":        d.plugins.List(),
			"models":         models,
			"temp_ttl_hours": int(d.sessions.TempTTL().Hours()),
		}, nil

	case "stop":
		go func() {
			time.Sleep(30 * time.Millisecond)
			d.Stop()
		}()
		return map[string]any{"stopping": true}, nil

	case "plugins.list":
		return d.plugins.ListDetailed(), nil

	case "plugins.install":
		url, _ := payload["url"].(string)
		targetDir := d.plugins.UserPluginDir()
		if targetDir == "" && len(d.opts.Layout.PluginDirs) > 0 {
			targetDir = d.opts.Layout.PluginDirs[0]
		}
		return d.plugins.InstallFromURL(ctx, url, targetDir)

	case "plugins.enable":
		id, _ := payload["id"].(string)
		enabled, _ := payload["enabled"].(bool)
		return d.plugins.SetEnabled(ctx, id, enabled)

	case "plugins.remove":
		id, _ := payload["id"].(string)
		return map[string]any{"deleted": id}, d.plugins.RemovePlugin(ctx, id)

	case "plugins.reload":
		if err := d.plugins.Reload(ctx); err != nil {
			return nil, err
		}
		return d.plugins.ListDetailed(), nil

	case "models.list":
		return d.registry.ListModels(ctx)

	case "sessions.list":
		return d.sessions.ListSessions()

	case "sessions.active":
		workdir, _ := payload["workdir"].(string)
		return d.sessions.ActiveForWorkdir(workdir)

	case "sessions.new":
		workdir, _ := payload["workdir"].(string)
		return d.sessions.NewSession(workdir)

	case "sessions.use":
		id, _ := payload["id"].(string)
		if id == "" {
			return nil, fmt.Errorf("session id is required")
		}
		return d.sessions.SetActiveSession(id)

	case "sessions.mode":
		workdir, _ := payload["workdir"].(string)
		kind, _ := payload["kind"].(string)
		return d.sessions.SetActiveSessionKind(workdir, kind)

	case "sessions.delete":
		id, _ := payload["id"].(string)
		if id == "" {
			return nil, fmt.Errorf("session id is required")
		}
		return map[string]any{"deleted": id}, d.sessions.DeleteSession(id)

	case "sessions.ttl":
		if h, ok := payload["hours"].(float64); ok && h > 0 {
			if err := d.sessions.SetTempTTLHours(int(h)); err != nil {
				return nil, err
			}
		}
		return map[string]any{"hours": int(d.sessions.TempTTL().Hours())}, nil

	case "agent.settings":
		return map[string]any{
			"max_steps":       d.sessions.MaxSteps(),
			"step_limit_mode": d.sessions.StepLimitMode(),
		}, nil

	case "agent.settings.save":
		if v, ok := payload["step_limit_mode"].(string); ok {
			if err := d.sessions.SetStepLimitMode(v); err != nil {
				return nil, err
			}
		}
		if v, ok := payload["max_steps"].(float64); ok {
			if err := d.sessions.SetMaxSteps(int(v)); err != nil {
				return nil, err
			}
		}
		return map[string]any{
			"max_steps":       d.sessions.MaxSteps(),
			"step_limit_mode": d.sessions.StepLimitMode(),
		}, nil

	case "turn.run":
		workdir, _ := payload["workdir"].(string)
		mode, _ := payload["mode"].(string)
		sessionID, _ := payload["session_id"].(string)
		model, _ := payload["model"].(string)
		preset, _ := payload["preset"].(string)
		prompt, _ := payload["prompt"].(string)

		return d.sessions.RunTurn(ctx, session.TurnRequest{
			Workdir:   workdir,
			Mode:      mode,
			SessionID: sessionID,
			Model:     model,
			Preset:    preset,
			Prompt:    prompt,
		}, func(ev session.TurnEvent) {
			_ = stream.Emit(ev)
		})

	default:
		return nil, fmt.Errorf("unknown ipc method %q", method)
	}
}
