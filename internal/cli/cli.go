// Package cli implements the command-line interface and daemon client for cdx.
package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sort"
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

func (a *App) runDaemonCmd(args []string) error {
	sub := "status"
	if len(args) > 0 {
		sub = args[0]
	}

	switch sub {
	case "run", "serve":
		httpAddr := os.Getenv("CDX_HTTP_ADDR")
		if httpAddr == "" {
			httpAddr = "127.0.0.1:8787"
		}
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
			fmt.Fprintf(a.Stdout, "%s %s  [%s]  msgs=%v  dir=%s\n", marker, s["id"], s["kind"], s["message_count"], s["workdir"])
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

	prompt := strings.TrimSpace(strings.Join(promptParts, " "))
	if prompt == "" {
		return errors.New("prompt is required")
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
				summary.recordEnd(ev)
				if spinner != nil {
					spinner.SetText(toolProgressLabel(ev) + " done")
				}
			}
		},
		OnPrompt: func(question map[string]any) (map[string]any, error) {
			if kind, _ := question["kind"].(string); kind == "max_steps" {
				if quiet || jsonOut || !isTTYOut {
					return map[string]any{"choice": "stop", "allow": false}, nil
				}
				if spinner != nil {
					spinner.Stop()
					spinner = nil
				}
				choice := a.askContinuePrompt(question)
				if spinner == nil && choice == "continue" {
					thinking.Reset()
					spinner = newProgressLine(true)
					spinner.SetText("continuing...")
				}
				return map[string]any{
					"choice": choice,
					"allow":  choice == "continue",
				}, nil
			}
			bell.ring()
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
			fmt.Fprint(a.Stderr, summary.format(resMap))
		}
	} else {
		raw, _ := json.Marshal(res)
		fmt.Fprintln(a.Stdout, string(raw))
	}
	bell.ring()
	return nil
}

func (a *App) askContinuePrompt(question map[string]any) string {
	inFile, _ := a.Stdin.(*os.File)
	if !isTerminal(inFile) {
		return "stop"
	}
	steps, _ := question["steps"].(float64)
	addSteps, _ := question["add_steps"].(float64)
	fmt.Fprintf(a.Stderr, "\nThe model made %d requests without a final answer.\n", int(steps))
	fmt.Fprintf(a.Stderr, "Continue with %d more steps? [Y/n]: ", int(addSteps))

	reader := bufio.NewReader(a.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && len(line) == 0 {
		return "continue"
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	if answer == "n" || answer == "no" {
		return "stop"
	}
	return "continue"
}

func (a *App) askPermissionPrompt(question map[string]any) string {
	tool, _ := question["tool"].(string)
	level, _ := question["permission"].(string)
	if level == "" {
		level, _ = question["level"].(string)
	}
	if level == "" {
		level = "edit"
	}
	summary, _ := question["summary"].(string)
	if summary == "" {
		summary, _ = question["question"].(string)
	}
	if summary == "" {
		summary = tool
	}

	options := []string{"once", "session", "daemon", "always", "deny"}
	if rawOpts, ok := question["options"].([]any); ok && len(rawOpts) > 0 {
		options = options[:0]
		for _, item := range rawOpts {
			if s, ok := item.(string); ok && s != "" {
				options = append(options, s)
			}
		}
	}

	clipped := clipText(summary, terminalWidth()-30)
	fmt.Fprintf(a.Stderr, "\npermission required: %s %s\n",
		styleAction(clipped), styleLevel(level))
	for i, opt := range options {
		fmt.Fprintf(a.Stderr, "  [%d] %s\n", i+1, opt)
	}
	fmt.Fprint(a.Stderr, "choose: ")

	reader := bufio.NewReader(a.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && len(line) == 0 {
		return "deny"
	}
	choice := strings.ToLower(strings.TrimSpace(line))
	if choice == "y" || choice == "yes" {
		return "once"
	}
	if choice == "n" || choice == "no" {
		return "deny"
	}
	for i, opt := range options {
		if choice == fmt.Sprintf("%d", i+1) || choice == strings.ToLower(opt) {
			return opt
		}
	}
	return "deny"
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
	toolCounts map[string]int
	changed    []string
	read       []string
	shell      []string
	errors     []string
}

func newTurnSummary() *turnSummary {
	return &turnSummary{toolCounts: map[string]int{}}
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
	if name == "" {
		name = "tool"
	}
	s.toolCounts[name]++
	args, _ := ev["tool_args"].(map[string]any)
	path, _ := args["path"].(string)
	switch name {
	case "write_file", "edit_file":
		s.changed = s.addUnique(s.changed, path)
	case "read_file", "list_dir":
		s.read = s.addUnique(s.read, path)
	case "shell":
		command, _ := args["command"].(string)
		s.shell = s.addUnique(s.shell, command)
	}
}

func (s *turnSummary) recordEnd(ev map[string]any) {
	out, _ := ev["tool_out"].(string)
	if !strings.HasPrefix(out, "error:") {
		return
	}
	name, _ := ev["tool_name"].(string)
	line := strings.TrimSpace(out)
	if len(line) > 200 {
		line = line[:200] + "..."
	}
	s.errors = s.addUnique(s.errors, name+": "+line)
}

func limited(items []string, n int) []string {
	if len(items) <= n {
		return items
	}
	return items[:n]
}

func formatList(title string, items []string) string {
	if len(items) == 0 {
		return ""
	}
	return title + ": " + strings.Join(items, ", ") + "\n"
}

func (s *turnSummary) format(res map[string]any) string {
	var b strings.Builder
	b.WriteString("---\n")

	steps := 0
	if v, ok := res["steps"].(float64); ok {
		steps = int(v)
	}
	content, _ := res["content"].(string)
	stopped, _ := res["stopped_reason"].(string)

	b.WriteString(fmt.Sprintf("Steps: %d\n", steps))

	if len(s.toolCounts) > 0 {
		names := make([]string, 0, len(s.toolCounts))
		for name := range s.toolCounts {
			names = append(names, name)
		}
		sort.Strings(names)
		parts := make([]string, 0, len(names))
		for _, name := range names {
			parts = append(parts, fmt.Sprintf("%s x%d", name, s.toolCounts[name]))
		}
		b.WriteString("Tools: " + strings.Join(parts, ", ") + "\n")
	}

	b.WriteString(formatList("Changed files", limited(s.changed, 5)))
	b.WriteString(formatList("Read paths", limited(s.read, 5)))
	b.WriteString(formatList("Shell commands", limited(s.shell, 3)))
	b.WriteString(formatList("Tool errors", limited(s.errors, 3)))

	if strings.TrimSpace(content) == "" {
		switch stopped {
		case "max_steps":
			b.WriteString("Stopped: the model reached the tool-step limit and did not produce a final answer\n")
		case "empty_answer":
			b.WriteString("Stopped: the model returned an empty final answer\n")
		default:
			b.WriteString("Stopped: the model did not produce a final answer\n")
		}
	} else if stopped == "max_steps" {
		b.WriteString("Stopped: the model reached the tool-step limit\n")
	}

	return b.String()
}

func (a *App) printUsage() {
	fmt.Fprintln(a.Stdout, `Usage:
  cdx [flags] "<prompt>"
  cdx daemon <start|stop|restart|status|run>
  cdx session <new|list|use|mode|delete>
  cdx plugins <list|reload>
  cdx models
  cdx api

Session commands:
  cdx session new                 Create a persistent session in the current directory
  cdx session list                List sessions and mark the active one
  cdx session use <id>            Select an existing session as active
  cdx session mode [temp|persistent]  Show or change the active session mode
  cdx session delete [id]         Delete a session (defaults to the active one)

Flags:
  -m, --model <name>     Model ID or name to use
  -p, --preset <id>      Agent preset ID (standard, mini, creator, or custom)
  -q, --quiet            Disable progress and the end-of-turn summary
      --json             Print the final result as JSON
      --no-color         Disable colored output
      --no-session       Run once without reading or saving session history`)
}
