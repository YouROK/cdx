package cli

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yourok/cdx/internal/config"
	"github.com/yourok/cdx/internal/daemon"
	"github.com/yourok/cdx/internal/i18n"
	"github.com/yourok/cdx/internal/logx"
)

// Main runs a command and returns the exit code.
func Main(args []string) int {
	o, err := Parse(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		Usage()
		return ExitGeneral
	}

	switch o.Command {
	case "help":
		Usage()
		return ExitOK
	case "version":
		PrintVersion()
		return ExitOK
	}

	cfg, err := Config(o)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ExitConfig
	}
	if err := SetupLogs(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "logging is disabled: %v\n", err)
	}
	defer logx.Close()

	// The language comes from the flag, then the config, then the environment.
	lang := cfg.Output.Language
	if o.Lang != "" {
		lang = o.Lang
	}
	if err := i18n.Init(lang); err != nil {
		// Localization must not stop the program from working.
		fmt.Fprintf(os.Stderr, "localization is disabled: %v\n", err)
	}

	switch o.Command {
	case "compact":
		return runCompactCommand(o, cfg)
	case "daemon":
		return runDaemonCommand(o, cfg)
	case "config":
		return runConfigCommand(o, cfg)
	case "session":
		return runSessionCommand(o, cfg)
	case "doctor":
		return runDoctor(o, cfg)
	case "plugin":
		return runPluginCommand(o, cfg)
	case "mcp":
		return runMCPCommand(o, cfg)
	case "run":
		return runPrompt(o, cfg)
	}

	Usage()
	return ExitGeneral
}

// runDaemonCommand serves the daemon commands.
func runDaemonCommand(o *Options, cfg *config.Config) int {
	if len(o.Args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: cdx daemon start|stop|status")
		return ExitGeneral
	}

	// --foreground keeps the daemon in the terminal, the way systemd runs it.
	foreground := o.Foreground
	for _, a := range o.Args[1:] {
		if a == "--foreground" {
			foreground = true
		}
	}

	switch o.Args[0] {
	case "serve":
		d, err := daemon.NewWithPath(cfg, configPath(o))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return ExitDaemon
		}
		installSignals(d)
		if err := d.Run(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return ExitDaemon
		}
		return ExitOK

	case "start":
		if foreground {
			d, err := daemon.NewWithPath(cfg, configPath(o))
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return ExitDaemon
			}
			installSignals(d)
			if err := d.Run(); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return ExitDaemon
			}
			return ExitOK
		}

		if IsRunning(cfg) {
			fmt.Println(i18n.Tr("cli.daemon.already_running"))
			return ExitOK
		}
		if err := spawnDaemon(cfg); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return ExitDaemon
		}
		if waitReady(cfg) {
			fmt.Println(i18n.Tr("cli.daemon.started"))
			return ExitOK
		}
		fmt.Fprintln(os.Stderr, i18n.Tr("cli.daemon.did_not_start"))
		return ExitDaemon

	case "stop":
		if !IsRunning(cfg) {
			fmt.Println(i18n.Tr("cli.daemon.not_running"))
			return ExitOK
		}
		c, err := connect(cfg)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return ExitDaemon
		}
		defer c.Close()
		_, _ = c.Call("stop", "daemon.stop", nil)
		fmt.Println(i18n.Tr("cli.daemon.stopped"))
		return ExitOK

	case "restart":
		// A restart replaces both the daemon and its web token, so the
		// interface is opened again with --config.
		if IsRunning(cfg) {
			if code := runDaemonCommand(&Options{Command: "daemon", Args: []string{"stop"}}, cfg); code != ExitOK {
				return code
			}
			// The socket is not freed instantly: without the wait the new
			// daemon may fail to take the address.
			waitDaemonStopped(cfg)
		}
		if code := runDaemonCommand(&Options{Command: "daemon", Args: []string{"start"}}, cfg); code != ExitOK {
			return code
		}
		fmt.Println(i18n.Tr("cli.daemon.restarted"))
		return ExitOK

	case "status":
		// Status never starts a daemon, else the check always says "running".
		c, err := connect(cfg)
		if err != nil {
			fmt.Println(i18n.Tr("cli.daemon.not_running"))
			return ExitDaemon
		}
		defer c.Close()

		resp, err := c.Call("status", "daemon.status", nil)
		if err != nil || resp.Error != nil {
			fmt.Println(i18n.Tr("cli.daemon.not_running"))
			return ExitDaemon
		}
		var st struct {
			State    string `json:"state"`
			Version  string `json:"version"`
			Protocol int    `json:"proto"`
			PID      int    `json:"pid"`
			Uptime   int64  `json:"uptime_s"`
		}
		if err := Decode(resp, &st); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return ExitDaemon
		}
		fmt.Printf("state: %s\nversion: %s\nprotocol: %d\npid: %d\nuptime: %ds\n",
			st.State, st.Version, st.Protocol, st.PID, st.Uptime)
		return ExitOK
	}

	fmt.Fprintf(os.Stderr, "unknown daemon command %q\n", o.Args[0])
	return ExitGeneral
}

// waitReady waits until the daemon is ready.
func waitReady(cfg *config.Config) bool {
	timeout := cfg.Daemon.StartTimeoutMS
	if timeout <= 0 {
		timeout = 10000
	}
	deadline := nowMillis() + int64(timeout)
	for nowMillis() < deadline {
		if IsRunning(cfg) {
			return true
		}
		sleepMillis(100)
	}
	return false
}

// installSignals shuts the daemon down on a signal.
func installSignals(d *daemon.Daemon) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-ch
		d.Stop()
	}()
}

// configPath returns the path of the config in use.
//
// A custom path comes from the CDX_CONFIG variable: there is no flag for
// it, since one would clash with the command that opens the settings.
func configPath(o *Options) string {
	p, err := config.DefaultPath()
	if err != nil {
		return ""
	}
	return p
}

// waitDaemonStopped waits for the daemon to release the socket.
func waitDaemonStopped(cfg *config.Config) {
	for i := 0; i < 50; i++ {
		if !IsRunning(cfg) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}
