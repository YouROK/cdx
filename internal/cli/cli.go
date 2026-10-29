// Package cli parses arguments and runs cdx commands.
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yourok/cdx/internal/config"
	"github.com/yourok/cdx/internal/logx"
	"github.com/yourok/cdx/internal/version"
)

// Exit codes.
const (
	ExitOK      = 0
	ExitGeneral = 1
	ExitConfig  = 2
	ExitDaemon  = 3
	ExitPerms   = 4
)

// Options is the parsed command line.
type Options struct {
	Lang string

	Quiet      bool
	JSON       bool
	NoColor    bool
	Model      string
	NoSess     bool
	Preset     string
	Allow      []string
	Foreground bool
	Force      bool
	Compact    bool

	Command string
	Args    []string
	Prompt  string
}

// Usage prints the help text.
func Usage() {
	fmt.Print(`cdx - command-line coding assistant

Usage:
  cdx [flags] <request>        run a one-shot request
  cdx --daemon start|stop|status|restart
  cdx --session new|end|list|show|use|delete
  cdx --plugin list|add|remove
  cdx --mcp list|add|remove
  cdx --config                 open the settings interface
  cdx --doctor                 check the environment
  cdx --compact                compact the history of the active session
  cdx --version                print version
  cdx --help                   show help

Everything after the first word that does not start with a dash is the
request, so quotes are not needed:
  cdx --model glm explain the code in main.go

A lone "--" stops flag parsing, so a request may start with a dash:
  cdx -- --json is a flag

Flags:
  -q, --quiet          no progress output
      --json           machine readable result
      --no-color       disable colors
      --model <name>   use a specific model
      --preset <id>    agent preset: standard, mini, creator or a custom one
      --no-session     ignore the active session
      --allow <level>  allow read|edit|dangerous for this call
      --lang <code>    message language: auto|en|ru
  -h, --help           show help
  -v, --version        print version
`)
}

// groups are commands with subcommands.
//
// Each is given by a flag and the subcommand follows it: cdx --session use <id>.
var groups = map[string]bool{
	"--daemon":  true,
	"--session": true,
	"--plugin":  true,
	"--mcp":     true,
}

// Parse parses the arguments.
//
// One scheme for every call: flags first, data after. The first word that
// does not start with a dash opens the request, and everything after it is
// part of the request, so quotes are not needed. A lone "--" stops flag
// parsing, since a request may itself start with a dash.
func Parse(args []string) (*Options, error) {
	o := &Options{}

	// Flags are parsed up to the first word without a dash.
	i := 0
	for ; i < len(args); i++ {
		arg := args[i]

		if arg == "--" {
			// From here on it is data, even if it looks like a flag.
			i++
			break
		}
		if !strings.HasPrefix(arg, "-") {
			break
		}

		// A command group takes the subcommand and its arguments whole.
		if groups[arg] {
			o.Command = strings.TrimPrefix(arg, "--")
			o.Args = args[i+1:]
			return o, nil
		}

		switch {
		case arg == "-h" || arg == "--help":
			o.Command = "help"
			return o, nil
		case arg == "-v" || arg == "--version":
			o.Command = "version"
			return o, nil
		case arg == "-q" || arg == "--quiet":
			o.Quiet = true
		case arg == "--json":
			o.JSON = true
		case arg == "--no-color":
			o.NoColor = true
		case arg == "--no-session":
			o.NoSess = true
		case arg == "--foreground":
			o.Foreground = true
		case arg == "--force":
			o.Force = true
		case arg == "--compact":
			o.Compact = true
		case arg == "--config":
			o.Command = "config"
			return o, nil
		case arg == "--doctor":
			o.Command = "doctor"
			return o, nil
		case arg == "--model" || arg == "--allow" || arg == "--lang" || arg == "--preset":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("%s requires a value", arg)
			}
			i++
			switch arg {
			case "--model":
				o.Model = args[i]
			case "--allow":
				o.Allow = append(o.Allow, args[i])
			case "--lang":
				o.Lang = args[i]
			case "--preset":
				o.Preset = args[i]
			}
		case strings.HasPrefix(arg, "--model="):
			o.Model = strings.TrimPrefix(arg, "--model=")
		case strings.HasPrefix(arg, "--allow="):
			o.Allow = append(o.Allow, strings.TrimPrefix(arg, "--allow="))
		case strings.HasPrefix(arg, "--lang="):
			o.Lang = strings.TrimPrefix(arg, "--lang=")
		case strings.HasPrefix(arg, "--preset="):
			o.Preset = strings.TrimPrefix(arg, "--preset=")
		default:
			return nil, fmt.Errorf("unknown flag %q", arg)
		}
	}

	// History compaction is an action of its own, not a request.
	if o.Compact {
		o.Command = "compact"
		return o, nil
	}

	if i >= len(args) {
		o.Command = "help"
		return o, nil
	}

	// Everything left is the request, including words that look like
	// flags.
	o.Command = "run"
	o.Prompt = strings.Join(args[i:], " ")
	return o, nil
}

// Config loads the configuration from the default path.
func Config(o *Options) (*config.Config, error) {
	path, err := config.DefaultPath()
	if err != nil {
		return nil, err
	}
	return config.Load(path)
}

// SetupLogs sets up logging from the config.
func SetupLogs(cfg *config.Config) error {
	dir, err := cfg.LogPath()
	if err != nil {
		return err
	}
	level, err := logx.ParseLevel(cfg.Logging.Level)
	if err != nil {
		return err
	}
	return logx.Init(logx.Options{
		Dir:      dir,
		Level:    level,
		MaxSize:  int64(cfg.Logging.MaxSizeMB) << 20,
		MaxFiles: cfg.Logging.MaxFiles,
	})
}

// PrintVersion prints the version.
func PrintVersion() {
	fmt.Printf("cdx %s (commit %s, built %s, proto %d)\n", version.Version, version.Commit, version.Date, version.Protocol)
}

// CWD returns the current directory as an absolute path.
func CWD() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return resolved
	}
	return dir
}

// sessionMode reports the session selection mode for the request.
//
// An explicit identifier is not accepted: a session is first made active
// with cdx --session use <id>.
func (o *Options) sessionMode() string {
	if o.NoSess {
		return "none"
	}
	return "auto"
}
