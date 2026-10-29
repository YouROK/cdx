// Package config describes the configuration, its defaults and validation.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CurrentVersion is the config format version.
const CurrentVersion = 1

// Config is the root of the config file.
type Config struct {
	Version  int          `yaml:"version"`
	Daemon   Daemon       `yaml:"daemon"`
	Web      Web          `yaml:"web"`
	Output   Output       `yaml:"output"`
	Agent    Agent        `yaml:"agent"`
	Context  Context      `yaml:"context"`
	Sessions Sessions     `yaml:"sessions"`
	Files    Files        `yaml:"files"`
	Perms    Permissions  `yaml:"permissions"`
	Danger   []DangerRule `yaml:"dangerous_rules"`
	Logging  Logging      `yaml:"logging"`
	Stats    Statistics   `yaml:"statistics"`
	Plugins  Plugins      `yaml:"plugins"`
	MCP      []MCPServer  `yaml:"mcp_servers"`
	// AgentPreset holds the active agent preset.
	AgentPreset Preset `yaml:"agent_preset"`
}

// Preset holds the agent preset settings.
//
// Custom presets live next to the system settings: there are few of them
// and they are worth seeing in the file as a whole.
type Preset struct {
	// Active is the id of the preset in use.
	Active string `yaml:"active"`
	// Custom holds the presets created by the user.
	Custom []CustomPreset `yaml:"custom"`
}

// CustomPreset is a user preset.
type CustomPreset struct {
	ID          string `yaml:"id"`
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Prompt      string `yaml:"prompt"`
}

// Daemon holds the daemon settings.
type Daemon struct {
	Socket            string `yaml:"socket"`
	PIDFile           string `yaml:"pid_file"`
	LockFile          string `yaml:"lock_file"`
	Autostart         bool   `yaml:"autostart"`
	StartTimeoutMS    int    `yaml:"start_timeout_ms"`
	ShutdownTimeoutMS int    `yaml:"shutdown_timeout_ms"`
	MaxFrameBytes     int    `yaml:"max_frame_bytes"`
}

// Web holds the web interface settings.
type Web struct {
	Enabled         bool   `yaml:"enabled"`
	Addr            string `yaml:"addr"`
	Port            int    `yaml:"port"`
	OpenBrowser     bool   `yaml:"open_browser"`
	TokenTTLMinutes int    `yaml:"token_ttl_minutes"`
}

// Output holds the output settings.
type Output struct {
	Color          string `yaml:"color"`
	Language       string `yaml:"language"`
	Spinner        bool   `yaml:"spinner"`
	ProgressMaxFPS int    `yaml:"progress_max_fps"`
	TruncateWidth  int    `yaml:"truncate_width"`
	// Bell rings when the reply ends.
	//
	// The plain BEL character is used: the terminal decides how to sound
	// it, so no separate mechanism is needed.
	Bell bool `yaml:"bell"`
}

// Agent holds the agent loop settings.
type Agent struct {
	Model              string   `yaml:"model"`
	MaxIterations      int      `yaml:"max_iterations"`
	TurnTimeoutMS      int      `yaml:"turn_timeout_ms"`
	LLMTimeoutMS       int      `yaml:"llm_timeout_ms"`
	LLMMaxRetries      int      `yaml:"llm_max_retries"`
	ToolResultMaxBytes int      `yaml:"tool_result_max_bytes"`
	PromptCache        bool     `yaml:"prompt_cache"`
	Subagents          Subagent `yaml:"subagents"`
}

// Subagent holds the subagent settings.
type Subagent struct {
	Enabled     bool `yaml:"enabled"`
	MaxParallel int  `yaml:"max_parallel"`
	TTLHours    int  `yaml:"ttl_hours"`
}

// Context holds the model context settings.
type Context struct {
	Strategy           string `yaml:"strategy"`
	RecentMessages     int    `yaml:"recent_messages"`
	RecentMessagesMin  int    `yaml:"recent_messages_min"`
	SummarizeThreshold int    `yaml:"summarize_threshold"`
	SummaryMaxTokens   int    `yaml:"summary_max_tokens"`
	KeepToolResults    bool   `yaml:"keep_tool_results"`
	SummarizeModel     string `yaml:"summarize_model"`
}

// Sessions holds the session settings.
type Sessions struct {
	Enabled            bool   `yaml:"enabled"`
	Marker             string `yaml:"marker"`
	MarkerGitignore    bool   `yaml:"marker_gitignore"`
	InheritFromParents bool   `yaml:"inherit_from_parents"`
	TTLDays            int    `yaml:"ttl_days"`
}

// Files holds the file handling settings.
type Files struct {
	MaxInlineSizeKB int      `yaml:"max_inline_size_kb"`
	FollowSymlinks  bool     `yaml:"follow_symlinks"`
	ContextFiles    []string `yaml:"context_files"`
}

// Permissions holds the default permission levels.
type Permissions struct {
	Read           string `yaml:"read"`
	Edit           string `yaml:"edit"`
	Dangerous      string `yaml:"dangerous"`
	NonInteractive string `yaml:"non_interactive"`
}

// DangerRule is a dangerous action rule.
type DangerRule struct {
	Name     string `yaml:"name"`
	Pattern  string `yaml:"pattern"`
	Severity string `yaml:"severity"`
}

// Logging holds the log settings.
type Logging struct {
	Level      string `yaml:"level"`
	Dir        string `yaml:"dir"`
	MaxSizeMB  int    `yaml:"max_size_mb"`
	MaxFiles   int    `yaml:"max_files"`
	MaxAgeDays int    `yaml:"max_age_days"`
	Compress   bool   `yaml:"compress"`
}

// Statistics holds the statistics settings.
type Statistics struct {
	Enabled bool `yaml:"enabled"`
	TTLDays int  `yaml:"ttl_days"`
}

// Plugins holds the plugin settings.
type Plugins struct {
	Enabled           bool         `yaml:"enabled"`
	Dir               string       `yaml:"dir"`
	InitTimeoutMS     int          `yaml:"init_timeout_ms"`
	ToolTimeoutMS     int          `yaml:"tool_timeout_ms"`
	ShutdownTimeoutMS int          `yaml:"shutdown_timeout_ms"`
	MaxCrashes        int          `yaml:"max_crashes"`
	Items             []PluginItem `yaml:"items"`
}

// PluginItem describes an installed plugin.
type PluginItem struct {
	Name      string `yaml:"name"`
	Enabled   bool   `yaml:"enabled"`
	Version   string `yaml:"version"`
	Source    string `yaml:"source"`
	TimeoutMS int    `yaml:"timeout_ms"`
}

// MCPServer describes an MCP server.
type MCPServer struct {
	Name        string            `yaml:"name"`
	Transport   string            `yaml:"transport"`
	Command     string            `yaml:"command"`
	Args        []string          `yaml:"args"`
	URL         string            `yaml:"url"`
	Enabled     bool              `yaml:"enabled"`
	TimeoutMS   int               `yaml:"timeout_ms"`
	MaxRetries  int               `yaml:"max_retries"`
	MaxCrashes  int               `yaml:"max_crashes"`
	AutoRestart bool              `yaml:"auto_restart"`
	Env         map[string]string `yaml:"env"`
	Dir         string            `yaml:"cwd"`
	Permission  string            `yaml:"permission"`
}

// Default returns a configuration with the default values.
func Default() *Config {
	return &Config{
		Version: CurrentVersion,
		Daemon: Daemon{
			Autostart:         true,
			StartTimeoutMS:    10000,
			ShutdownTimeoutMS: 5000,
			MaxFrameBytes:     16 << 20,
		},
		Web: Web{
			Enabled:         true,
			Addr:            "127.0.0.1",
			Port:            7777,
			OpenBrowser:     true,
			TokenTTLMinutes: 0,
		},
		Output: Output{
			Color:          "auto",
			Language:       "auto",
			Spinner:        true,
			ProgressMaxFPS: 10,
		},
		Agent: Agent{
			MaxIterations:      50,
			TurnTimeoutMS:      600000,
			LLMTimeoutMS:       180000,
			LLMMaxRetries:      3,
			ToolResultMaxBytes: 32768,
			PromptCache:        true,
			Subagents: Subagent{
				Enabled:     true,
				MaxParallel: 4,
				TTLHours:    24,
			},
		},
		Context: Context{
			Strategy:           "hybrid",
			RecentMessages:     20,
			RecentMessagesMin:  4,
			SummarizeThreshold: 50,
			SummaryMaxTokens:   2000,
			KeepToolResults:    true,
		},
		Sessions: Sessions{
			Enabled:            true,
			Marker:             "none",
			InheritFromParents: true,
		},
		Files: Files{
			MaxInlineSizeKB: 50,
			ContextFiles:    []string{"agent.md", "skills.md"},
		},
		Perms: Permissions{
			Read:           "allow",
			Edit:           "ask",
			Dangerous:      "ask",
			NonInteractive: "deny",
		},
		Danger: []DangerRule{
			{Name: "delete-files", Pattern: `rm\s+(-[a-zA-Z]*r[a-zA-Z]*f|-[a-zA-Z]*f[a-zA-Z]*r)\b`, Severity: "critical"},
			{Name: "force-push", Pattern: `git\s+push.*--force`, Severity: "high"},
			{Name: "disk-format", Pattern: `(mkfs|dd\s+.*of=/dev/)`, Severity: "critical"},
		},
		Logging: Logging{
			Level:      "info",
			MaxSizeMB:  10,
			MaxFiles:   5,
			MaxAgeDays: 14,
		},
		Stats: Statistics{Enabled: true, TTLDays: 365},
		Plugins: Plugins{
			Enabled:           true,
			InitTimeoutMS:     5000,
			ToolTimeoutMS:     60000,
			ShutdownTimeoutMS: 2000,
			MaxCrashes:        3,
			Items:             []PluginItem{},
		},
		MCP: []MCPServer{},
		AgentPreset: Preset{
			Active: "standard",
		},
	}
}

// Load reads the config at the path, creating it when missing.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("read config %s: %w", path, err)
		}
		cfg := Default()
		if err := cfg.Save(path); err != nil {
			return nil, err
		}
		return cfg, nil
	}

	cfg, err := Parse(data)
	if err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// DefaultPath returns the default config path.
func DefaultPath() (string, error) {
	if p := os.Getenv("CDX_CONFIG"); p != "" {
		return expand(p)
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve config dir: %w", err)
	}
	return filepath.Join(base, "cdx", "config.yaml"), nil
}

// expand replaces a leading "~" in the path.
func expand(p string) (string, error) {
	if p == "" {
		return "", nil
	}
	if strings.HasPrefix(p, "~/") || p == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home dir: %w", err)
		}
		if p == "~" {
			return home, nil
		}
		return filepath.Join(home, p[2:]), nil
	}
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("path %q must be absolute or start with ~", p)
	}
	return p, nil
}

// ExpandPath replaces "~" and makes the path absolute.
func ExpandPath(p string) (string, error) {
	return expand(p)
}

// DataDir returns the data directory.
func DataDir() (string, error) {
	if p := os.Getenv("CDX_DATA_DIR"); p != "" {
		return expand(p)
	}
	base, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(base, ".local", "share", "cdx"), nil
}

// Duration converts milliseconds to a duration.
func MS(v int) time.Duration {
	if v <= 0 {
		return 0
	}
	return time.Duration(v) * time.Millisecond
}
