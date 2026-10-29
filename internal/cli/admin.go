package cli

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/yourok/cdx/internal/config"
	"github.com/yourok/cdx/internal/i18n"
	"github.com/yourok/cdx/internal/ipc"
	"github.com/yourok/cdx/internal/version"
)

// runConfigCommand opens the web interface.
func runConfigCommand(o *Options, cfg *config.Config) int {
	c, err := Dial(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitDaemon
	}
	defer c.Close()

	resp, err := c.Call("w1", "web.url", nil)
	if err != nil || resp.Error != nil {
		fmt.Fprintln(os.Stderr, callError(resp, err))
		return ExitDaemon
	}

	var body struct {
		URL string `json:"url"`
	}
	if err := Decode(resp, &body); err != nil || body.URL == "" {
		fmt.Fprintln(os.Stderr, callError(resp, err))
		return ExitGeneral
	}

	fmt.Println(body.URL)
	if cfg.Web.OpenBrowser {
		if err := openBrowser(body.URL); err != nil {
			// The browser is optional: the URL is already printed.
			fmt.Fprintf(os.Stderr, "failed to open browser: %v\n", err)
		}
	}
	return ExitOK
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

// runSessionCommand serves the session commands.
func runSessionCommand(o *Options, cfg *config.Config) int {
	if len(o.Args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: cdx session new|end|list|show|use|delete")
		return ExitGeneral
	}

	c, err := Dial(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitDaemon
	}
	defer c.Close()

	switch o.Args[0] {
	case "new":
		title := strings.Join(o.Args[1:], " ")
		resp, err := c.Call("s1", "session.new", map[string]any{"cwd": CWD(), "title": title})
		if err != nil || resp.Error != nil {
			fmt.Fprintln(os.Stderr, callError(resp, err))
			return ExitDaemon
		}
		var body struct {
			Session sessionInfo `json:"session"`
		}
		if err := Decode(resp, &body); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return ExitGeneral
		}
		fmt.Println(i18n.Tr("cli.session.created", body.Session.ID, body.Session.Root))
		return ExitOK

	case "end":
		resp, err := c.Call("s1", "session.end", map[string]any{"cwd": CWD()})
		if err != nil || resp.Error != nil {
			fmt.Fprintln(os.Stderr, callError(resp, err))
			return ExitDaemon
		}
		fmt.Println(i18n.Tr("cli.session.ended"))
		return ExitOK

	case "list":
		resp, err := c.Call("s1", "session.list", map[string]any{"cwd": CWD()})
		if err != nil || resp.Error != nil {
			fmt.Fprintln(os.Stderr, callError(resp, err))
			return ExitDaemon
		}
		var body struct {
			Sessions      []sessionInfo `json:"sessions"`
			InheritedFrom string        `json:"inherited_from"`
		}
		if err := Decode(resp, &body); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return ExitGeneral
		}
		if len(body.Sessions) == 0 {
			fmt.Println(i18n.Tr("cli.session.none"))
			if body.InheritedFrom != "" {
				fmt.Println(i18n.Tr("cli.session.inherited", body.InheritedFrom))
			}
			return ExitOK
		}
		fmt.Printf("%-26s %-9s %-9s %-6s %s\n", "ID", "MESSAGES", "STATUS", "UPDATED", "TITLE")
		for _, s := range body.Sessions {
			status := ""
			if s.Active {
				status = "active"
			}
			if s.Inherited {
				status = "inherited"
			}
			fmt.Printf("%-26s %-9d %-9s %-6s %s\n",
				s.ID, s.Messages, status, ageOf(s.UpdatedAt), s.Title)
		}
		if body.InheritedFrom != "" {
			fmt.Printf("\nactive session is inherited from %s\n", body.InheritedFrom)
		}
		return ExitOK

	case "show":
		payload := map[string]any{"cwd": CWD()}
		if len(o.Args) > 1 {
			payload["id"] = o.Args[1]
		}
		resp, err := c.Call("s1", "session.show", payload)
		if err != nil || resp.Error != nil {
			fmt.Fprintln(os.Stderr, callError(resp, err))
			return ExitDaemon
		}
		var body struct {
			Session  *sessionInfo  `json:"session"`
			Messages []messageInfo `json:"messages"`
		}
		if err := Decode(resp, &body); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return ExitGeneral
		}
		if body.Session == nil {
			fmt.Println(i18n.Tr("cli.session.no_active"))
			return ExitOK
		}
		fmt.Printf("session %s (%s), %d messages\n", body.Session.ID, body.Session.Title, len(body.Messages))
		if body.Session.Model != "" {
			fmt.Printf("model: %s\n", body.Session.Model)
		}
		fmt.Println()
		for _, msg := range body.Messages {
			printMessage(msg, o.JSON)
		}
		return ExitOK

	case "use":
		if len(o.Args) < 2 {
			fmt.Fprintln(os.Stderr, "session id is required")
			return ExitGeneral
		}
		resp, err := c.Call("s1", "session.use", map[string]any{"id": o.Args[1]})
		if err != nil || resp.Error != nil {
			fmt.Fprintln(os.Stderr, callError(resp, err))
			return ExitDaemon
		}
		fmt.Println(i18n.Tr("cli.session.active", o.Args[1]))
		return ExitOK

	case "delete":
		payload := map[string]any{"cwd": CWD()}
		if len(o.Args) > 1 {
			payload["id"] = o.Args[1]
		}
		resp, err := c.Call("s1", "session.delete", payload)
		if err != nil || resp.Error != nil {
			fmt.Fprintln(os.Stderr, callError(resp, err))
			return ExitDaemon
		}
		var body struct {
			Deleted string `json:"deleted"`
		}
		_ = Decode(resp, &body)
		fmt.Println(i18n.Tr("cli.session.deleted", body.Deleted))
		return ExitOK
	}

	fmt.Fprintf(os.Stderr, "unknown session command %q\n", o.Args[0])
	return ExitGeneral
}

// sessionInfo is a session in the daemon's answer.
type sessionInfo struct {
	ID        string `json:"id"`
	Root      string `json:"root"`
	Title     string `json:"title"`
	Model     string `json:"model"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
	Messages  int    `json:"messages"`
	Active    bool   `json:"active"`
	Inherited bool   `json:"inherited"`
}

// messageInfo is a history entry in the daemon's answer.
type messageInfo struct {
	Seq        int    `json:"seq"`
	Kind       string `json:"kind"`
	Content    string `json:"content"`
	ToolName   string `json:"tool_name"`
	Model      string `json:"model"`
	TokensIn   int    `json:"tokens_in"`
	TokensOut  int    `json:"tokens_out"`
	CoversFrom int    `json:"covers_from"`
	CoversTo   int    `json:"covers_to"`
	TS         int64  `json:"ts"`
}

// printMessage prints a single history entry.
func printMessage(msg messageInfo, asJSON bool) {
	if asJSON {
		return
	}
	switch msg.Kind {
	case "summary":
		fmt.Printf("--- summary (messages %d-%d) ---\n%s\n\n", msg.CoversFrom, msg.CoversTo, msg.Content)
	case "user":
		fmt.Printf("> %s\n\n", msg.Content)
	case "assistant":
		meta := ""
		if msg.Model != "" {
			meta = fmt.Sprintf(" [%s, %d/%d tokens]", msg.Model, msg.TokensIn, msg.TokensOut)
		}
		fmt.Printf("%s%s\n\n", msg.Content, meta)
	case "tool_call":
		fmt.Printf("  -> %s %s\n", msg.ToolName, strings.TrimSpace(msg.Content))
	case "tool_result":
		preview := strings.TrimSpace(msg.Content)
		if idx := strings.IndexByte(preview, '\n'); idx >= 0 {
			preview = preview[:idx]
		}
		if len(preview) > 100 {
			preview = preview[:100] + "..."
		}
		fmt.Printf("  <- %s\n", preview)
	}
}

// callError returns the error text of a call.
func callError(resp *ipc.Frame, err error) string {
	if err != nil {
		return err.Error()
	}
	if resp != nil && resp.Error != nil {
		return resp.Error.Message
	}
	return "call failed"
}

// ageOf turns a timestamp into a short age label.
func ageOf(ts int64) string {
	if ts == 0 {
		return "-"
	}
	d := time.Since(time.Unix(ts, 0))
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// runDoctor checks the environment.
func runDoctor(o *Options, cfg *config.Config) int {
	problems := 0

	report := func(name, status, detail string) {
		fmt.Printf("%-14s %-6s %s\n", name, status, detail)
		if status == "FAIL" {
			problems++
		}
	}

	ok := i18n.Tr("cli.doctor.ok")
	warn := i18n.Tr("cli.doctor.warn")
	failMark := i18n.Tr("cli.doctor.fail")

	report(i18n.Tr("cli.doctor.config"), ok, "loaded")

	socket, err := cfg.SocketPath()
	if err != nil {
		report(i18n.Tr("cli.doctor.socket"), failMark, err.Error())
	} else if info, err := os.Stat(socket); err != nil {
		report(i18n.Tr("cli.doctor.socket"), warn, "not present, daemon is not running")
	} else {
		report(i18n.Tr("cli.doctor.socket"), ok, fmt.Sprintf("%s (%o)", info.Name(), info.Mode().Perm()))
	}

	if IsRunning(cfg) {
		report(i18n.Tr("cli.doctor.daemon"), ok, "running")
	} else {
		report(i18n.Tr("cli.doctor.daemon"), warn, "not running")
	}

	dir, err := config.DataDir()
	if err != nil {
		report(i18n.Tr("cli.doctor.data"), failMark, err.Error())
	} else if err := os.MkdirAll(dir, 0o700); err != nil {
		report(i18n.Tr("cli.doctor.data"), failMark, err.Error())
	} else {
		report(i18n.Tr("cli.doctor.data"), ok, dir)
	}

	dbPath, err := cfg.DBPath()
	if err == nil {
		if info, err := os.Stat(dbPath); err == nil {
			report(i18n.Tr("cli.doctor.database"), ok, fmt.Sprintf("%d bytes", info.Size()))
		} else {
			report(i18n.Tr("cli.doctor.database"), warn, "not created yet")
		}
	}

	logs, err := cfg.LogPath()
	if err == nil {
		if err := os.MkdirAll(logs, 0o700); err != nil {
			report(i18n.Tr("cli.doctor.logs"), failMark, err.Error())
		} else {
			report(i18n.Tr("cli.doctor.logs"), ok, logs)
		}
	}

	report(i18n.Tr("cli.doctor.version"), ok, fmt.Sprintf("%s, proto %d", version.Version, version.Protocol))

	if problems > 0 {
		return ExitConfig
	}
	return ExitOK
}

// DescribeFrame returns a short frame description for debugging.
func DescribeFrame(f *ipc.Frame) string {
	return strings.Join([]string{f.Type, f.ID, f.Method}, "/")
}

// runPluginCommand serves the plugin commands.
func runPluginCommand(o *Options, cfg *config.Config) int {
	if len(o.Args) == 0 || o.Args[0] == "list" {
		c, err := Dial(cfg)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return ExitDaemon
		}
		defer c.Close()

		resp, err := c.Call("p1", "plugins.list", nil)
		if err != nil || resp.Error != nil {
			fmt.Fprintln(os.Stderr, callError(resp, err))
			return ExitDaemon
		}

		var body struct {
			Plugins []struct {
				Name         string   `json:"name"`
				Version      string   `json:"version"`
				Description  string   `json:"description"`
				Author       string   `json:"author"`
				Capabilities []string `json:"capabilities"`
				Tools        []string `json:"tools"`
			} `json:"plugins"`
		}
		if err := Decode(resp, &body); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return ExitGeneral
		}

		if len(body.Plugins) == 0 {
			fmt.Println("no plugins loaded")
			return ExitOK
		}

		for _, p := range body.Plugins {
			fmt.Printf("%s %s\n", p.Name, p.Version)
			if p.Description != "" {
				fmt.Printf("  %s\n", p.Description)
			}
			if len(p.Capabilities) > 0 {
				fmt.Printf("  capabilities: %s\n", strings.Join(p.Capabilities, ", "))
			}
			if len(p.Tools) > 0 {
				fmt.Printf("  tools: %s\n", strings.Join(p.Tools, ", "))
			}
		}
		return ExitOK
	}

	fmt.Fprintf(os.Stderr, "unknown plugin command %q\n", o.Args[0])
	return ExitGeneral
}

// runMCPCommand serves the MCP commands.
func runMCPCommand(o *Options, cfg *config.Config) int {
	c, err := Dial(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitDaemon
	}
	defer c.Close()

	if len(o.Args) > 0 && o.Args[0] == "restart" {
		if len(o.Args) < 2 {
			fmt.Fprintln(os.Stderr, "server name is required")
			return ExitGeneral
		}
		resp, err := c.Call("m1", "mcp.restart", map[string]any{"name": o.Args[1]})
		if err != nil || resp.Error != nil {
			fmt.Fprintln(os.Stderr, callError(resp, err))
			return ExitDaemon
		}
		fmt.Printf("server %s restarted\n", o.Args[1])
		return ExitOK
	}

	resp, err := c.Call("m1", "mcp.list", nil)
	if err != nil || resp.Error != nil {
		fmt.Fprintln(os.Stderr, callError(resp, err))
		return ExitDaemon
	}

	var body struct {
		Servers []struct {
			Name      string `json:"name"`
			Transport string `json:"transport"`
			Enabled   bool   `json:"enabled"`
			Connected bool   `json:"connected"`
			Tools     int    `json:"tools"`
			Failed    bool   `json:"failed"`
		} `json:"servers"`
	}
	if err := Decode(resp, &body); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitGeneral
	}

	if len(body.Servers) == 0 {
		fmt.Println("no MCP servers configured")
		return ExitOK
	}

	fmt.Printf("%-16s %-8s %-8s %-6s %s\n", "NAME", "TRANSPORT", "STATUS", "TOOLS", "ENABLED")
	for _, s := range body.Servers {
		status := "off"
		if s.Connected {
			status = "ok"
		} else if s.Failed {
			status = "failed"
		}
		enabled := "no"
		if s.Enabled {
			enabled = "yes"
		}
		fmt.Printf("%-16s %-8s %-8s %-6d %s\n", s.Name, s.Transport, status, s.Tools, enabled)
	}
	return ExitOK
}

// runCompactCommand forces compaction of the active session history.
func runCompactCommand(o *Options, cfg *config.Config) int {
	c, err := Dial(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitDaemon
	}
	defer c.Close()

	// The directory comes from the client: the daemon lives in its own, and
	// the active session has to be looked up where the command was run.
	payload := map[string]any{"session": map[string]any{"mode": "auto"}, "cwd": CWD()}

	resp, err := c.Call("c1", "session.compact", payload)
	if err != nil || resp.Error != nil {
		fmt.Fprintln(os.Stderr, callError(resp, err))
		return ExitGeneral
	}

	var body struct {
		SessionID string `json:"session_id"`
		Compacted int    `json:"compacted"`
	}
	if err := Decode(resp, &body); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitGeneral
	}
	fmt.Println(i18n.Tr("cli.compact.done", body.Compacted, body.SessionID))
	return ExitOK
}
