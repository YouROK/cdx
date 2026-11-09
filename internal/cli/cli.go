// Package cli implements the command-line interface and daemon client for cdx.
package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/yourok/cdx/internal/daemon"
	"github.com/yourok/cdx/internal/ipc"
	"github.com/yourok/cdx/internal/logx"
	"github.com/yourok/cdx/internal/paths"
	"github.com/yourok/cdx/internal/plugin"
	"github.com/yourok/cdx/internal/session"
	"github.com/yourok/cdx/internal/version"
)

// App holds the CLI execution environment.
type App struct {
	Layout *paths.Layout
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// OpenBrowser opens a URL in the default browser; nil uses the system opener.
	OpenBrowser func(url string) error
}

// NewApp creates a CLI application with standard I/O streams.
func NewApp(layout *paths.Layout) *App {
	return &App{
		Layout: layout,
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}
}

// Run parses command-line arguments and executes the requested command or prompt.
func (a *App) Run(args []string) error {
	if a.Layout == nil {
		l, err := paths.Resolve()
		if err != nil {
			return err
		}
		a.Layout = l
	}

	if len(args) == 0 {
		a.printUsage()
		return nil
	}

	switch args[0] {
	case "version", "--version", "-v":
		fmt.Fprintf(a.Stdout, "cdx %s (protocol v%d)\n", version.Version, version.Protocol)
		return nil

	case "help", "--help", "-h":
		a.printUsage()
		return nil

	case "--config":
		return a.runConfigCmd(args[1:])

	case "daemon":
		return a.runDaemonCmd(args[1:])

	case "session", "sessions":
		return a.runSessionCmd(args[1:])

	case "plugins", "plugin":
		return a.runPluginsCmd(args[1:])

	case "models", "model":
		return a.runModelsCmd()

	case "api", "api-doc":
		fmt.Fprint(a.Stdout, plugin.RenderAPIDoc())
		return nil
	}

	return a.runPromptCmd(args)
}

// runConfigCmd opens the dashboard settings page in the default browser.
func (a *App) runConfigCmd(args []string) error {
	if len(args) > 0 {
		return errors.New("--config takes no arguments")
	}
	if err := a.ensureDaemon(); err != nil {
		return err
	}
	res, err := ipc.Call(context.Background(), a.Layout.SocketPath, "status", nil, ipc.ClientCallbacks{})
	if err != nil {
		return fmt.Errorf("daemon status: %w", err)
	}
	status, _ := res.(map[string]any)
	addr, _ := status["http_addr"].(string)
	url := settingsURL(addr)
	if url == "" {
		return errors.New("the daemon has no HTTP interface")
	}

	fmt.Fprintln(a.Stdout, url)
	opener := a.OpenBrowser
	if opener == nil {
		opener = openBrowser
	}
	if err := opener(url); err != nil {
		// The browser is optional: the URL is already printed.
		fmt.Fprintf(a.Stderr, "Warning: failed to open browser: %v\n", err)
	}
	return nil
}

func (a *App) runDaemonCmd(args []string) error {
	sub := "status"
	if len(args) > 0 {
		sub = args[0]
	}

	switch sub {
	case "run", "serve":
		httpAddr := defaultHTTPAddr()
		for i := 1; i < len(args); i++ {
			if (args[i] == "--http" || args[i] == "--addr") && i+1 < len(args) {
				httpAddr = args[i+1]
				i++
			}
		}

		logger := logx.New()
		_ = logger.Init(logx.Options{
			Dir:    a.Layout.LogDir,
			Level:  logx.LevelInfo,
			Mirror: true,
			Stderr: a.Stderr,
		})
		defer logger.Close()

		d, err := daemon.New(context.Background(), daemon.Options{
			Layout:   a.Layout,
			HTTPAddr: httpAddr,
			Logger:   logger,
		})
		if err != nil {
			return err
		}
		if err := d.Start(); err != nil {
			return err
		}

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-sigCh
			d.Stop()
		}()

		d.Wait()
		return nil

	case "start":
		if err := a.ensureDaemon(); err != nil {
			return err
		}
		res, err := ipc.Call(context.Background(), a.Layout.SocketPath, "status", nil, ipc.ClientCallbacks{})
		if err == nil {
			if m, ok := res.(map[string]any); ok {
				fmt.Fprintf(a.Stdout, "Daemon is running (http://%v)\n", m["http_addr"])
				return nil
			}
		}
		fmt.Fprintln(a.Stdout, "Daemon is running")
		return nil

	case "stop":
		if err := a.stopDaemon(); err != nil {
			return fmt.Errorf("daemon is not running: %w", err)
		}
		fmt.Fprintln(a.Stdout, "Daemon stopped")
		return nil

	case "restart":
		_ = a.stopDaemon()
		if err := a.ensureDaemon(); err != nil {
			return err
		}
		res, err := ipc.Call(context.Background(), a.Layout.SocketPath, "status", nil, ipc.ClientCallbacks{})
		if err == nil {
			if m, ok := res.(map[string]any); ok {
				fmt.Fprintf(a.Stdout, "Daemon restarted (version %v, http http://%v)\n", m["version"], m["http_addr"])
				return nil
			}
		}
		fmt.Fprintln(a.Stdout, "Daemon restarted")
		return nil

	case "status":
		res, err := ipc.Call(context.Background(), a.Layout.SocketPath, "status", nil, ipc.ClientCallbacks{})
		if err != nil {
			fmt.Fprintln(a.Stdout, "Daemon status: stopped")
			return nil
		}
		m, _ := res.(map[string]any)
		fmt.Fprintf(a.Stdout, "Daemon status: running (version %v, http http://%v)\n", m["version"], m["http_addr"])
		return nil

	default:
		return fmt.Errorf("unknown daemon command %q (expected: run, start, stop, restart, status)", sub)
	}
}

func (a *App) runSessionCmd(args []string) error {
	if err := a.ensureDaemon(); err != nil {
		return err
	}
	sub := "list"
	if len(args) > 0 {
		sub = args[0]
	}
	workdir, _ := os.Getwd()

	switch sub {
	case "new":
		res, err := ipc.Call(context.Background(), a.Layout.SocketPath, "sessions.new", map[string]any{"workdir": workdir}, ipc.ClientCallbacks{})
		if err != nil {
			return err
		}
		s, _ := res.(map[string]any)
		fmt.Fprintf(a.Stdout, "Created persistent session %v for %s\n", s["id"], workdir)
		return nil

	case "list", "ls":
		res, err := ipc.Call(context.Background(), a.Layout.SocketPath, "sessions.list", nil, ipc.ClientCallbacks{})
		if err != nil {
			return err
		}
		list, _ := res.([]any)
		if len(list) == 0 {
			fmt.Fprintln(a.Stdout, "No sessions")
			return nil
		}
		activeRaw, _ := ipc.Call(context.Background(), a.Layout.SocketPath, "sessions.active", map[string]any{"workdir": workdir}, ipc.ClientCallbacks{})
		active, _ := activeRaw.(map[string]any)
		activeID, _ := active["id"].(string)
		for _, item := range list {
			s, _ := item.(map[string]any)
			marker := " "
			if id, _ := s["id"].(string); id == activeID {
				marker = "*"
			}
			suffix := ""
			if parent, _ := s["parent_id"].(string); parent != "" {
				suffix = "  parent=" + parent
			}
			fmt.Fprintf(a.Stdout, "%s %s  [%s]  msgs=%v  dir=%s%s\n", marker, s["id"], s["kind"], s["message_count"], s["workdir"], suffix)
		}
		return nil

	case "use":
		if len(args) < 2 {
			return errors.New("session id is required")
		}
		res, err := ipc.Call(context.Background(), a.Layout.SocketPath, "sessions.use", map[string]any{"id": args[1]}, ipc.ClientCallbacks{})
		if err != nil {
			return err
		}
		s, _ := res.(map[string]any)
		fmt.Fprintf(a.Stdout, "Active session %v for %s\n", s["id"], s["workdir"])
		return nil

	case "mode":
		if len(args) < 2 {
			res, err := ipc.Call(context.Background(), a.Layout.SocketPath, "sessions.active", map[string]any{"workdir": workdir}, ipc.ClientCallbacks{})
			if err != nil {
				return err
			}
			s, _ := res.(map[string]any)
			if len(s) == 0 {
				fmt.Fprintf(a.Stdout, "No active session for %s\n", workdir)
				return nil
			}
			fmt.Fprintf(a.Stdout, "Session %v mode: %v\n", s["id"], s["kind"])
			return nil
		}
		kind := args[1]
		res, err := ipc.Call(context.Background(), a.Layout.SocketPath, "sessions.mode", map[string]any{"workdir": workdir, "kind": kind}, ipc.ClientCallbacks{})
		if err != nil {
			return err
		}
		s, _ := res.(map[string]any)
		fmt.Fprintf(a.Stdout, "Session %v is now %v for %s\n", s["id"], s["kind"], s["workdir"])
		return nil

	case "delete", "rm":
		id := ""
		if len(args) > 1 {
			id = args[1]
		} else {
			activeRaw, err := ipc.Call(context.Background(), a.Layout.SocketPath, "sessions.active", map[string]any{"workdir": workdir}, ipc.ClientCallbacks{})
			if err != nil {
				return err
			}
			active, _ := activeRaw.(map[string]any)
			id, _ = active["id"].(string)
			if id == "" {
				return fmt.Errorf("no active session for %s", workdir)
			}
		}
		if _, err := ipc.Call(context.Background(), a.Layout.SocketPath, "sessions.delete", map[string]any{"id": id}, ipc.ClientCallbacks{}); err != nil {
			return err
		}
		fmt.Fprintf(a.Stdout, "Deleted session %s\n", id)
		return nil

	default:
		return fmt.Errorf("unknown session command %q (expected: new, list, use, mode, delete)", sub)
	}
}

func (a *App) runPluginsCmd(args []string) error {
	if err := a.ensureDaemon(); err != nil {
		return err
	}
	sub := "list"
	if len(args) > 0 {
		sub = args[0]
	}

	switch sub {
	case "list", "ls":
		res, err := ipc.Call(context.Background(), a.Layout.SocketPath, "plugins.list", nil, ipc.ClientCallbacks{})
		if err != nil {
			return err
		}
		list, _ := res.([]any)
		if len(list) == 0 {
			fmt.Fprintln(a.Stdout, "No plugins loaded")
			return nil
		}
		for _, item := range list {
			p, _ := item.(map[string]any)
			state := "enabled"
			if en, ok := p["enabled"].(bool); ok && !en {
				state = "disabled"
			}
			fmt.Fprintf(a.Stdout, "%-20s v%-8v [%-8s] (%v) %s\n", p["id"], p["version"], state, p["scope"], p["description"])
		}
		return nil

	case "install":
		if len(args) < 2 {
			return errors.New("zip url is required: cdx plugins install <url.zip>")
		}
		res, err := ipc.Call(context.Background(), a.Layout.SocketPath, "plugins.install", map[string]any{"url": args[1]}, ipc.ClientCallbacks{})
		if err != nil {
			return err
		}
		p, _ := res.(map[string]any)
		fmt.Fprintf(a.Stdout, "Installed plugin %v v%v\n", p["id"], p["version"])
		return nil

	case "enable":
		if len(args) < 2 {
			return errors.New("plugin id is required")
		}
		_, err := ipc.Call(context.Background(), a.Layout.SocketPath, "plugins.enable", map[string]any{"id": args[1], "enabled": true}, ipc.ClientCallbacks{})
		if err != nil {
			return err
		}
		fmt.Fprintf(a.Stdout, "Enabled plugin %s\n", args[1])
		return nil

	case "disable":
		if len(args) < 2 {
			return errors.New("plugin id is required")
		}
		_, err := ipc.Call(context.Background(), a.Layout.SocketPath, "plugins.enable", map[string]any{"id": args[1], "enabled": false}, ipc.ClientCallbacks{})
		if err != nil {
			return err
		}
		fmt.Fprintf(a.Stdout, "Disabled plugin %s\n", args[1])
		return nil

	case "remove", "delete", "rm":
		if len(args) < 2 {
			return errors.New("plugin id is required")
		}
		_, err := ipc.Call(context.Background(), a.Layout.SocketPath, "plugins.remove", map[string]any{"id": args[1]}, ipc.ClientCallbacks{})
		if err != nil {
			return err
		}
		fmt.Fprintf(a.Stdout, "Removed plugin %s\n", args[1])
		return nil

	case "reload":
		_, err := ipc.Call(context.Background(), a.Layout.SocketPath, "plugins.reload", nil, ipc.ClientCallbacks{})
		if err != nil {
			return err
		}
		fmt.Fprintln(a.Stdout, "Plugins reloaded")
		return nil

	default:
		return fmt.Errorf("unknown plugins command %q (expected: list, install, enable, disable, remove, reload)", sub)
	}
}

func (a *App) runModelsCmd() error {
	if err := a.ensureDaemon(); err != nil {
		return err
	}
	res, err := ipc.Call(context.Background(), a.Layout.SocketPath, "models.list", nil, ipc.ClientCallbacks{})
	if err != nil {
		return err
	}
	list, _ := res.([]any)
	if len(list) == 0 {
		fmt.Fprintln(a.Stdout, "No models configured")
		return nil
	}
	for _, item := range list {
		m, _ := item.(map[string]any)
		def := " "
		if isDef, _ := m["default"].(bool); isDef {
			def = "*"
		}
		fmt.Fprintf(a.Stdout, "%s %-24s (plugin: %v)\n", def, m["id"], m["plugin_id"])
	}
	return nil
}

func (a *App) runPromptCmd(args []string) error {
	mode := session.ModeAuto
	model := ""
	preset := ""
	noColor := false
	quiet := false
	jsonOut := false
	promptFile := ""
	promptFileSet := false
	var promptParts []string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--session" || arg == "-s" || strings.HasPrefix(arg, "--session="):
			return errors.New("--session was removed; use the cdx session command")
		case arg == "--no-session":
			mode = session.ModeNone
		case arg == "--model" || arg == "-m":
			if i+1 >= len(args) {
				return errors.New("--model requires a model name")
			}
			model = args[i+1]
			i++
		case strings.HasPrefix(arg, "--model="):
			model = strings.TrimPrefix(arg, "--model=")
		case arg == "--preset" || arg == "-p":
			if i+1 >= len(args) {
				return errors.New("--preset requires a preset id")
			}
			preset = args[i+1]
			i++
		case strings.HasPrefix(arg, "--preset="):
			preset = strings.TrimPrefix(arg, "--preset=")
		case arg == "--prompt-file" || arg == "-f":
			if i+1 >= len(args) {
				return errors.New("--prompt-file requires a path")
			}
			promptFile = args[i+1]
			promptFileSet = true
			i++
		case strings.HasPrefix(arg, "--prompt-file="):
			promptFile = strings.TrimPrefix(arg, "--prompt-file=")
			promptFileSet = true
		case arg == "--no-color":
			noColor = true
			disableColor()
		case arg == "-q" || arg == "--quiet":
			quiet = true
		case arg == "--json":
			jsonOut = true
		default:
			promptParts = append(promptParts, arg)
		}
	}

	if promptFileSet && promptFile == "" {
		return errors.New("--prompt-file requires a path")
	}
	prompt, err := promptSource(a.Stdin, promptFile, promptParts)
	if err != nil {
		return err
	}

	if err := a.ensureDaemon(); err != nil {
		return err
	}

	outFile, _ := a.Stdout.(*os.File)
	isTTYOut := isTerminal(outFile)
	bell := newBell(!quiet && !jsonOut, a.Stdout)

	workdir, _ := os.Getwd()

	var spinner *progressLine
	if isTTYOut && !noColor && !quiet && !jsonOut {
		spinner = newProgressLine(true)
		defer func() {
			if spinner != nil {
				spinner.Stop()
			}
		}()
	}

	var thinking strings.Builder
	summary := newTurnSummary()
	turnStart := time.Now()
	res, err := ipc.Call(context.Background(), a.Layout.SocketPath, "turn.run", map[string]any{
		"workdir": workdir,
		"mode":    mode,
		"model":   model,
		"preset":  preset,
		"prompt":  prompt,
	}, ipc.ClientCallbacks{
		OnEvent: func(data any) {
			ev, ok := data.(map[string]any)
			if !ok {
				return
			}
			switch ev["type"] {
			case "waiting":
				if spinner != nil {
					spinner.SetText("waiting for the running turn in this session...")
				}
			case "session":
				thinking.Reset()
				if spinner != nil {
					spinner.SetText("asking model...")
				}
			case "reasoning":
				delta, _ := ev["delta"].(string)
				if delta == "" {
					return
				}
				thinking.WriteString(delta)
				if spinner != nil {
					spinner.SetText(thinkingTail(thinking.String()))
				}
			case "tool_start":
				thinking.Reset()
				summary.recordStart(ev)
				if spinner != nil {
					spinner.SetText(toolProgressLabel(ev))
				}
			case "text":
				if spinner != nil {
					spinner.SetText("writing answer...")
				}
			case "tool_end":
				if spinner != nil {
					spinner.SetText(toolProgressLabel(ev) + " done")
				}
			}
		},
		OnPrompt: func(question map[string]any) (map[string]any, error) {
			bell.ring()
			if _, ok := question["source"].(string); ok {
				if quiet || jsonOut || !isTTYOut {
					return map[string]any{"cancelled": true, "reason": "non-interactive"}, nil
				}
				if spinner != nil {
					spinner.Stop()
					spinner = nil
				}
				answer := a.askQuestionPrompt(question)
				if isTTYOut && !noColor && !quiet && !jsonOut {
					spinner = newProgressLine(true)
				}
				return answer, nil
			}
			if spinner != nil {
				spinner.Stop()
				spinner = nil
			}
			choice := a.askPermissionPrompt(question)
			if isTTYOut && !noColor && !quiet && !jsonOut {
				spinner = newProgressLine(true)
			}
			return map[string]any{
				"choice": choice,
				"allow":  choice != "deny",
			}, nil
		},
	})
	if spinner != nil {
		spinner.Stop()
		spinner = nil
	}
	if err != nil {
		return err
	}

	if jsonOut {
		raw, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(a.Stdout, string(raw))
	} else if resMap, ok := res.(map[string]any); ok {
		if content, _ := resMap["content"].(string); content != "" {
			color := !noColor && isTTYOut
			fmt.Fprint(a.Stdout, newRenderer(color).Render(content))
		}
		if isTTYOut && !quiet && !jsonOut {
			fmt.Fprint(a.Stderr, summary.format(resMap, time.Since(turnStart)))
		}
	} else {
		raw, _ := json.Marshal(res)
		fmt.Fprintln(a.Stdout, string(raw))
	}
	bell.ring()
	return nil
}

// promptSource builds the prompt from an optional file and positional text.
func promptSource(stdin io.Reader, file string, parts []string) (string, error) {
	text := strings.Join(parts, " ")
	if file != "" {
		content, err := readPromptFile(stdin, file)
		if err != nil {
			return "", err
		}
		text = strings.TrimSpace(content) + " " + text
	}
	prompt := strings.TrimSpace(text)
	if prompt == "" {
		return "", errors.New("prompt is required")
	}
	return prompt, nil
}

// readPromptFile reads a prompt from a file, or from stdin when path is "-".
func readPromptFile(stdin io.Reader, path string) (string, error) {
	if path == "-" {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("failed to read prompt from stdin: %w", err)
		}
		return string(data), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read prompt file: %w", err)
	}
	return string(data), nil
}

func (a *App) askQuestionPrompt(question map[string]any) map[string]any {
	p := buildQuestionPrompt(question, terminalWidth())
	inFile, _ := a.Stdin.(*os.File)
	if !isTerminal(inFile) {
		return map[string]any{"cancelled": true, "reason": "not interactive"}
	}
	return a.readQuestionAnswer(p)
}

// readQuestionAnswer prints the question and repeats the prompt until the answer matches it.
func (a *App) readQuestionAnswer(p questionPrompt) map[string]any {
	fmt.Fprintln(a.Stderr)
	for _, line := range p.lines {
		fmt.Fprintln(a.Stderr, line)
	}

	reader := bufio.NewReader(a.Stdin)
	for {
		fmt.Fprint(a.Stderr, p.prompt)
		line, err := reader.ReadString('\n')
		if err != nil && strings.TrimSpace(line) == "" {
			return map[string]any{"cancelled": true, "reason": "no answer"}
		}
		answer, ok := parseQuestionAnswer(line, p)
		if ok {
			return answer
		}
		fmt.Fprintln(a.Stderr, "Unknown answer, try again.")
	}
}

func (a *App) askPermissionPrompt(question map[string]any) string {
	p := buildPermissionPrompt(question, terminalWidth())
	fmt.Fprintln(a.Stderr)
	for _, line := range p.lines {
		fmt.Fprintln(a.Stderr, line)
	}
	fmt.Fprint(a.Stderr, p.prompt)

	reader := bufio.NewReader(a.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return "deny"
	}
	return parsePromptAnswer(line, p)
}

func (a *App) stopDaemon() error {
	_, err := ipc.Call(context.Background(), a.Layout.SocketPath, "stop", nil, ipc.ClientCallbacks{})
	if err != nil {
		return err
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		_, pingErr := ipc.Call(ctx, a.Layout.SocketPath, "status", nil, ipc.ClientCallbacks{})
		cancel()
		if pingErr != nil {
			return nil
		}
		time.Sleep(40 * time.Millisecond)
	}
	return nil
}

// newDaemonCommand builds the autostart command with stdio bound to /dev/null.
// A non-nil io.Writer makes os/exec create a pipe that closes with this process
// and kills the daemon with SIGPIPE on its next log write.
func newDaemonCommand(exe string) *exec.Cmd {
	cmd := exec.Command(exe, "daemon", "run")
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd
}

func (a *App) ensureDaemon() error {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	_, err := ipc.Call(ctx, a.Layout.SocketPath, "status", nil, ipc.ClientCallbacks{})
	cancel()
	if err == nil {
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	cmd := newDaemonCommand(exe)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("autostart daemon: %w", err)
	}
	_ = cmd.Process.Release()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		cCtx, cCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		_, pingErr := ipc.Call(cCtx, a.Layout.SocketPath, "status", nil, ipc.ClientCallbacks{})
		cCancel()
		if pingErr == nil {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("timed out waiting for daemon to start")
}

// defaultHTTPAddr returns the listener address for the daemon HTTP interface.
// CDX_ALLOW_REMOTE_HOST opens the listener to the network when CDX_HTTP_ADDR is unset.
func defaultHTTPAddr() string {
	if addr := os.Getenv("CDX_HTTP_ADDR"); addr != "" {
		return addr
	}
	if envEnabled("CDX_ALLOW_REMOTE_HOST") {
		return "0.0.0.0:8787"
	}
	return "127.0.0.1:8787"
}

// envEnabled reports whether a boolean environment variable is set to a value
// other than an explicit negative.
func envEnabled(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "", "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

// settingsURL builds the dashboard URL from the daemon HTTP address.
func settingsURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return ""
	}
	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// openBrowser launches the system browser.
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

func thinkingTail(text string) string {
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.Join(strings.Fields(text), " ")
	text = strings.TrimPrefix(text, "<think>")
	text = strings.TrimSpace(text)

	const limit = 90
	if runes := []rune(text); len(runes) > limit {
		return "..." + string(runes[len(runes)-limit:])
	}
	return text
}

func toolProgressLabel(ev map[string]any) string {
	name, _ := ev["tool_name"].(string)
	if name == "" {
		name = "tool"
	}
	args, _ := ev["tool_args"].(map[string]any)
	for _, key := range []string{"command", "path", "query", "pattern", "url", "file", "filename"} {
		if value, ok := args[key].(string); ok && value != "" {
			return name + " " + value
		}
	}
	return name
}

type turnSummary struct {
	changed []string
}

func newTurnSummary() *turnSummary {
	return &turnSummary{}
}

func (s *turnSummary) addUnique(list []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return list
	}
	for _, item := range list {
		if item == value {
			return list
		}
	}
	return append(list, value)
}

func (s *turnSummary) recordStart(ev map[string]any) {
	name, _ := ev["tool_name"].(string)
	args, _ := ev["tool_args"].(map[string]any)
	path, _ := args["path"].(string)
	switch name {
	case "write_file", "edit_file":
		s.changed = s.addUnique(s.changed, path)
	}
}

// format renders the short end-of-turn line: duration, steps, tokens, changes.
func (s *turnSummary) format(res map[string]any, elapsed time.Duration) string {
	var b strings.Builder
	b.WriteString("---\n")

	parts := []string{"Done in " + formatDuration(elapsed), fmt.Sprintf("%d steps", int(numberField(res, "steps")))}
	tokensIn, tokensOut := sessionTokens(res)
	if tokensIn > 0 || tokensOut > 0 {
		parts = append(parts, fmt.Sprintf("%s in / %s out tokens", formatTokens(tokensIn), formatTokens(tokensOut)))
	}
	b.WriteString(strings.Join(parts, ", ") + "\n")

	if len(s.changed) > 0 {
		b.WriteString("Changed: " + s.changedLine() + "\n")
	}

	content, _ := res["content"].(string)
	if strings.TrimSpace(content) == "" {
		switch stopped, _ := res["stopped_reason"].(string); stopped {
		case "max_steps":
			b.WriteString("Stopped: the model reached the tool-step limit and did not produce a final answer\n")
		case "empty_answer":
			b.WriteString("Stopped: the model returned an empty final answer\n")
		default:
			b.WriteString("Stopped: the model did not produce a final answer\n")
		}
	}
	return b.String()
}

// changedLine lists a few changed files by name, so the line stays short.
func (s *turnSummary) changedLine() string {
	names := make([]string, 0, len(s.changed))
	for _, path := range s.changed {
		names = append(names, filepath.Base(path))
	}
	shown := names
	if len(shown) > 3 {
		shown = shown[:3]
	}
	line := fmt.Sprintf("%d files", len(names))
	if len(names) == 1 {
		line = "1 file"
	}
	line += " (" + strings.Join(shown, ", ")
	if len(names) > len(shown) {
		line += fmt.Sprintf(", +%d more", len(names)-len(shown))
	}
	return line + ")"
}

func sessionTokens(res map[string]any) (int64, int64) {
	session, _ := res["session"].(map[string]any)
	if session == nil {
		return 0, 0
	}
	return int64(numberField(session, "tokens_in")), int64(numberField(session, "tokens_out"))
}

func numberField(values map[string]any, key string) float64 {
	switch value := values[key].(type) {
	case float64:
		return value
	case int:
		return float64(value)
	case int64:
		return float64(value)
	case json.Number:
		parsed, _ := value.Float64()
		return parsed
	}
	return 0
}

func formatDuration(elapsed time.Duration) string {
	if elapsed < time.Second {
		elapsed = time.Second
	}
	elapsed = elapsed.Round(time.Second)
	if elapsed < time.Minute {
		return fmt.Sprintf("%ds", int(elapsed.Seconds()))
	}
	if elapsed < time.Hour {
		minutes := int(elapsed / time.Minute)
		seconds := int(elapsed.Seconds()) % 60
		if seconds == 0 {
			return fmt.Sprintf("%dm", minutes)
		}
		return fmt.Sprintf("%dm%ds", minutes, seconds)
	}
	hours := int(elapsed / time.Hour)
	minutes := int(elapsed/time.Minute) % 60
	if minutes == 0 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dh%dm", hours, minutes)
}

func formatTokens(count int64) string {
	switch {
	case count < 1000:
		return strconv.FormatInt(count, 10)
	case count < 1000000:
		return trimZero(float64(count)/1000) + "k"
	default:
		return trimZero(float64(count)/1000000) + "M"
	}
}

func trimZero(value float64) string {
	return strings.TrimSuffix(strconv.FormatFloat(value, 'f', 1, 64), ".0")
}

func (a *App) printUsage() {
	fmt.Fprintln(a.Stdout, `Usage:
  cdx [flags] "<prompt>"
  cdx daemon <start|stop|restart|status|run>
  cdx session <new|list|use|mode|delete>
  cdx plugins <list|reload>
  cdx models
  cdx api
  cdx --config                   Open the settings page in the browser

Session commands:
  cdx session new                 Create a persistent session in the current directory
  cdx session list                List sessions and mark the active one
  cdx session use <id>            Select an existing session as active
  cdx session mode [temp|persistent]  Show or change the active session mode
  cdx session delete [id]         Delete a session (defaults to the active one)

Flags:
  -m, --model <name>     Model ID or name to use
  -p, --preset <id>      Agent preset ID (standard, mini, creator, or custom)
  -f, --prompt-file <path>  Read the prompt from a file ("-" reads stdin)
  -q, --quiet            Disable progress and the end-of-turn summary
      --json             Print the final result as JSON
      --no-color         Disable colored output
      --no-session       Run once without reading or saving session history`)
}
