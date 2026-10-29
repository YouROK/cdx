package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Parse decodes YAML into the configuration.
func Parse(data []byte) (*Config, error) {
	cfg := Default()
	dec := yaml.NewDecoder(bytes.NewReader(data))
	// Fields from future versions must not break startup.
	dec.KnownFields(false)
	if err := dec.Decode(cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.Version > CurrentVersion {
		return nil, fmt.Errorf("config version %d is newer than supported %d", cfg.Version, CurrentVersion)
	}
	if cfg.Version < CurrentVersion {
		if err := cfg.migrate(); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

// Save writes the config to a file, creating the directory when needed.
func (c *Config) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	var buf bytes.Buffer
	buf.WriteString("# cdx configuration\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(c); err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("encode config: %w", err)
	}

	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// migrate brings an older config version up to the current one.
func (c *Config) migrate() error {
	switch c.Version {
	case 0:
		c.Version = 1
	default:
		return fmt.Errorf("no migration path from config version %d", c.Version)
	}
	return nil
}

var (
	reName  = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)
	reLevel = regexp.MustCompile(`^(allow|ask|deny)$`)
)

// Validate checks the whole configuration.
func (c *Config) Validate() error {
	if c.Version != CurrentVersion {
		return fmt.Errorf("version: expected %d, got %d", CurrentVersion, c.Version)
	}

	if err := oneOf("output.color", c.Output.Color, "auto", "always", "never"); err != nil {
		return err
	}
	if err := oneOf("output.language", c.Output.Language, "auto", "en", "ru"); err != nil {
		return err
	}
	if err := oneOf("context.strategy", c.Context.Strategy, "hybrid", "recent"); err != nil {
		return err
	}
	if err := oneOf("sessions.marker", c.Sessions.Marker, "none", "pointer"); err != nil {
		return err
	}
	if err := oneOf("logging.level", c.Logging.Level, "debug", "info", "warn", "error"); err != nil {
		return err
	}
	if err := oneOf("web.addr", c.Web.Addr, "127.0.0.1", "localhost"); err != nil {
		return err
	}

	if err := validLevel("permissions.read", c.Perms.Read); err != nil {
		return err
	}
	if err := validLevel("permissions.edit", c.Perms.Edit); err != nil {
		return err
	}
	if err := validLevel("permissions.dangerous", c.Perms.Dangerous); err != nil {
		return err
	}
	if err := oneOf("permissions.non_interactive", c.Perms.NonInteractive, "allow", "deny"); err != nil {
		return err
	}

	if c.Daemon.StartTimeoutMS <= 0 {
		return fmt.Errorf("daemon.start_timeout_ms: must be positive")
	}
	if c.Daemon.MaxFrameBytes < 1024 {
		return fmt.Errorf("daemon.max_frame_bytes: must be at least 1024")
	}
	if c.Agent.MaxIterations <= 0 {
		return fmt.Errorf("agent.max_iterations: must be positive")
	}
	if c.Context.RecentMessages <= 0 {
		return fmt.Errorf("context.recent_messages: must be positive")
	}
	if c.Context.RecentMessagesMin <= 0 {
		return fmt.Errorf("context.recent_messages_min: must be positive")
	}
	if c.Files.MaxInlineSizeKB <= 0 {
		return fmt.Errorf("files.max_inline_size_kb: must be positive")
	}
	if c.Web.Port < 0 || c.Web.Port > 65535 {
		return fmt.Errorf("web.port: must be in 0..65535")
	}

	for i, r := range c.Danger {
		if r.Name == "" {
			return fmt.Errorf("dangerous_rules[%d].name: required", i)
		}
		if r.Pattern == "" {
			return fmt.Errorf("dangerous_rules[%d].pattern: required", i)
		}
		if _, err := regexp.Compile(r.Pattern); err != nil {
			return fmt.Errorf("dangerous_rules[%d].pattern: %w", i, err)
		}
		if err := oneOf(fmt.Sprintf("dangerous_rules[%d].severity", i), r.Severity, "low", "medium", "high", "critical"); err != nil {
			return err
		}
	}

	for i, m := range c.MCP {
		if m.Name == "" {
			return fmt.Errorf("mcp_servers[%d].name: required", i)
		}
		if !reName.MatchString(m.Name) {
			return fmt.Errorf("mcp_servers[%d].name: invalid characters", i)
		}
		if err := oneOf(fmt.Sprintf("mcp_servers[%d].transport", i), m.Transport, "stdio", "http"); err != nil {
			return err
		}
		if m.Transport == "stdio" && m.Command == "" {
			return fmt.Errorf("mcp_servers[%d].command: required for stdio", i)
		}
		if m.Transport == "http" && m.URL == "" {
			return fmt.Errorf("mcp_servers[%d].url: required for http", i)
		}
	}

	for i, p := range c.Plugins.Items {
		if !reName.MatchString(p.Name) {
			return fmt.Errorf("plugins.items[%d].name: invalid characters", i)
		}
	}

	for _, name := range c.Files.ContextFiles {
		if strings.ContainsAny(name, `/\`) || name == ".." {
			return fmt.Errorf("files.context_files: %q must be a plain file name", name)
		}
	}

	return nil
}

// oneOf checks that the value is in the allowed set.
func oneOf(field, value string, allowed ...string) error {
	for _, a := range allowed {
		if value == a {
			return nil
		}
	}
	return fmt.Errorf("%s: expected one of %s, got %q", field, strings.Join(allowed, "|"), value)
}

// validLevel checks the permission levels.
func validLevel(field, value string) error {
	if !reLevel.MatchString(value) {
		return fmt.Errorf("%s: expected allow|ask|deny, got %q", field, value)
	}
	return nil
}

// SocketPath returns the daemon socket path.
func (c *Config) SocketPath() (string, error) {
	if c.Daemon.Socket != "" {
		return ExpandPath(c.Daemon.Socket)
	}
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "cdx.sock"), nil
}

// PIDPath returns the daemon pid file path.
func (c *Config) PIDPath() (string, error) {
	if c.Daemon.PIDFile != "" {
		return ExpandPath(c.Daemon.PIDFile)
	}
	sock, err := c.SocketPath()
	if err != nil {
		return "", err
	}
	return sock + ".pid", nil
}

// LockPath returns the autostart lock file path.
func (c *Config) LockPath() (string, error) {
	if c.Daemon.LockFile != "" {
		return ExpandPath(c.Daemon.LockFile)
	}
	sock, err := c.SocketPath()
	if err != nil {
		return "", err
	}
	return sock + ".lock", nil
}

// DBPath returns the database path.
func (c *Config) DBPath() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "cdx.db"), nil
}

// LogPath returns the log directory path.
func (c *Config) LogPath() (string, error) {
	if c.Logging.Dir != "" {
		return ExpandPath(c.Logging.Dir)
	}
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "logs"), nil
}

// PluginsPath returns the plugin directory path.
func (c *Config) PluginsPath() (string, error) {
	if c.Plugins.Dir != "" {
		return ExpandPath(c.Plugins.Dir)
	}
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "plugins"), nil
}
