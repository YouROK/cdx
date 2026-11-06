// Package paths resolves XDG and system directories used by the daemon and CLI.
package paths

import (
	"fmt"
	"os"
	"path/filepath"
)

// SystemPluginDir is the default directory where system plugins are installed.
const SystemPluginDir = "/usr/share/cdx/plugins"

// Layout holds all resolved filesystem paths for a cdx instance.
type Layout struct {
	ConfigDir     string
	DataDir       string
	StateDir      string
	RuntimeDir    string
	LogDir        string
	DBPath        string
	MasterKeyPath string
	SocketPath    string
	PIDPath       string
	PluginDirs    []string
}

// Resolve builds the standard path layout using environment variables and XDG defaults.
func Resolve() (*Layout, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home directory: %w", err)
	}
	return ResolveWithHome(home, os.Getenv), nil
}

// ResolveWithHome builds the path layout from an explicit home directory and env lookup function.
func ResolveWithHome(home string, getenv func(string) string) *Layout {
	configBase := firstNonEmpty(getenv("CDX_CONFIG_DIR"), joinIf(getenv("XDG_CONFIG_HOME"), "cdx"), filepath.Join(home, ".config", "cdx"))
	dataBase := firstNonEmpty(getenv("CDX_DATA_DIR"), joinIf(getenv("XDG_DATA_HOME"), "cdx"), filepath.Join(home, ".local", "share", "cdx"))
	stateBase := firstNonEmpty(getenv("CDX_STATE_DIR"), joinIf(getenv("XDG_STATE_HOME"), "cdx"), filepath.Join(home, ".local", "state", "cdx"))

	runtimeBase := getenv("CDX_RUNTIME_DIR")
	if runtimeBase == "" {
		if xdgRun := getenv("XDG_RUNTIME_DIR"); xdgRun != "" {
			runtimeBase = filepath.Join(xdgRun, "cdx")
		} else {
			runtimeBase = filepath.Join(stateBase, "run")
		}
	}

	userPlugins := filepath.Join(dataBase, "plugins")
	sysPlugins := firstNonEmpty(getenv("CDX_SYSTEM_PLUGIN_DIR"), SystemPluginDir)

	pluginDirs := []string{userPlugins}
	if sysPlugins != userPlugins {
		pluginDirs = append(pluginDirs, sysPlugins)
	}

	return &Layout{
		ConfigDir:     configBase,
		DataDir:       dataBase,
		StateDir:      stateBase,
		RuntimeDir:    runtimeBase,
		LogDir:        filepath.Join(stateBase, "logs"),
		DBPath:        filepath.Join(dataBase, "cdx.db"),
		MasterKeyPath: filepath.Join(dataBase, "master.key"),
		SocketPath:    filepath.Join(runtimeBase, "cdx.sock"),
		PIDPath:       filepath.Join(runtimeBase, "cdx.pid"),
		PluginDirs:    pluginDirs,
	}
}

// EnsureDirs creates the required data, state, log, runtime, and user plugin directories.
func (l *Layout) EnsureDirs() error {
	dirs := []string{
		l.ConfigDir,
		l.DataDir,
		l.StateDir,
		l.RuntimeDir,
		l.LogDir,
	}
	if len(l.PluginDirs) > 0 {
		dirs = append(dirs, l.PluginDirs[0])
	}
	for _, d := range dirs {
		if d == "" {
			continue
		}
		if err := os.MkdirAll(d, 0o700); err != nil {
			return fmt.Errorf("create directory %s: %w", d, err)
		}
	}
	return nil
}

func joinIf(base, elem string) string {
	if base == "" {
		return ""
	}
	return filepath.Join(base, elem)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
