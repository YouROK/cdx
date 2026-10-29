package web

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/yourok/cdx/internal/store"
)

// ProviderEdit is a provider change from the interface.
type ProviderEdit struct {
	// Original is the previous name, used to find the record on rename.
	Original string `json:"original"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	BaseURL  string `json:"base_url"`
	APIKey   string `json:"api_key"`
}

// ModelEdit is a model change from the interface.
type ModelEdit struct {
	Provider      string            `json:"provider"`
	Original      string            `json:"original"`
	Name          string            `json:"name"`
	Remote        string            `json:"remote"`
	ContextWindow int               `json:"context_window"`
	MaxTokens     int               `json:"max_tokens"`
	Temperature   float64           `json:"temperature"`
	Default       bool              `json:"default"`
	Reasoning     map[string]string `json:"reasoning"`
	ReasoningDef  string            `json:"reasoning_default"`
	AskReasoning  bool              `json:"supports_reasoning_effort"`
	MaxTokenField string            `json:"max_tokens_field"`
	NoTemperature bool              `json:"skip_temperature"`
}

// PluginView is a plugin for the interface.
type PluginView struct {
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Description  string   `json:"description"`
	Author       string   `json:"author"`
	Homepage     string   `json:"homepage"`
	Permission   string   `json:"permission"`
	MinCDX       string   `json:"min_cdx_version"`
	Capabilities []string `json:"capabilities"`
	// Tools holds tool names for the list row.
	Tools []string `json:"tools"`
	// ToolDetails holds full descriptions for the details modal.
	ToolDetails []ToolView `json:"tool_details"`
	Loaded      bool       `json:"loaded"`
	Enabled     bool       `json:"enabled"`
}

// ToolView is a plugin tool for the details modal.
type ToolView struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Permission  string `json:"permission"`
}

// MCPView is an MCP server for the interface.
type MCPView struct {
	Name        string            `json:"name"`
	Transport   string            `json:"transport"`
	Command     string            `json:"command"`
	Args        []string          `json:"args"`
	URL         string            `json:"url"`
	Enabled     bool              `json:"enabled"`
	TimeoutMS   int               `json:"timeout_ms"`
	Permission  string            `json:"permission"`
	Env         map[string]string `json:"env"`
	Dir         string            `json:"cwd"`
	AutoRestart bool              `json:"auto_restart"`
	Connected   bool              `json:"connected"`
	Tools       int               `json:"tools"`
	Failed      bool              `json:"failed"`
}

// MCPEdit is a server change from the interface.
type MCPEdit struct {
	Original    string            `json:"original"`
	Name        string            `json:"name"`
	Transport   string            `json:"transport"`
	Command     string            `json:"command"`
	Args        []string          `json:"args"`
	URL         string            `json:"url"`
	Enabled     bool              `json:"enabled"`
	TimeoutMS   int               `json:"timeout_ms"`
	Permission  string            `json:"permission"`
	Env         map[string]string `json:"env"`
	Dir         string            `json:"cwd"`
	AutoRestart bool              `json:"auto_restart"`
}

// DiscoveredModelView is a model from the provider list.
type DiscoveredModelView struct {
	ID            string `json:"id"`
	ContextWindow int    `json:"context_window"`
	MaxTokens     int    `json:"max_tokens"`
	// Configured means the model is already set up for this provider.
	Configured bool `json:"configured"`
	// LocalName is the saved name when the model is configured.
	LocalName string `json:"local_name,omitempty"`
}

// RunningView is a running request for the interface.
type RunningView struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Preset  string `json:"preset"`
	Prompt  string `json:"prompt"`
	CWD     string `json:"cwd"`
	Started int64  `json:"started"`
}

// PresetView is an agent preset for the interface.
type PresetView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Prompt      string `json:"prompt"`
	Builtin     bool   `json:"builtin"`
	Active      bool   `json:"active"`
}

// PresetEdit is a preset change from the interface.
type PresetEdit struct {
	Original    string `json:"original"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Prompt      string `json:"prompt"`
}

// DangerRuleView is a danger rule for the interface.
type DangerRuleView struct {
	Name     string `json:"name"`
	Pattern  string `json:"pattern"`
	Severity string `json:"severity"`
}

// ProviderView is a provider with its models for the interface.
type ProviderView struct {
	Name     string      `json:"name"`
	Protocol string      `json:"protocol"`
	BaseURL  string      `json:"base_url"`
	HasKey   bool        `json:"has_key"`
	Models   []ModelView `json:"models"`
}

// ModelView is a model for the interface.
type ModelView struct {
	Provider       string            `json:"provider"`
	Name           string            `json:"name"`
	Remote         string            `json:"remote"`
	Default        bool              `json:"default"`
	ContextWindow  int               `json:"context_window"`
	MaxTokens      int               `json:"max_tokens"`
	Temperature    float64           `json:"temperature"`
	Reasoning      []string          `json:"reasoning_levels"`
	ReasoningMap   map[string]string `json:"reasoning"`
	ReasoningDef   string            `json:"reasoning_default"`
	AskReasoning   bool              `json:"supports_reasoning_effort"`
	MaxTokensField string            `json:"max_tokens_field"`
	SkipTemp       bool              `json:"skip_temperature"`
}

// listProviders returns providers together with their models.
func (s *Server) listProviders(c *gin.Context) {
	if s.deps.Providers == nil {
		c.JSON(http.StatusOK, gin.H{"providers": []ProviderView{}})
		return
	}
	list, err := s.deps.Providers()
	if err != nil {
		fail(c, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"providers": list})
}

// saveProvider creates or updates a provider.
func (s *Server) saveProvider(c *gin.Context) {
	var body ProviderEdit
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, http.StatusBadRequest, "bad_params", err.Error())
		return
	}
	if body.Name == "" {
		fail(c, http.StatusBadRequest, "bad_params", "name is required")
		return
	}
	if s.deps.SaveProvider == nil {
		fail(c, http.StatusNotImplemented, "unsupported", "provider editing is not available")
		return
	}
	if err := s.deps.SaveProvider(body); err != nil {
		fail(c, http.StatusBadRequest, "provider_invalid", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"saved": true})
}

// deleteProvider removes a provider together with its models.
func (s *Server) deleteProvider(c *gin.Context) {
	if s.deps.DeleteProvider == nil {
		fail(c, http.StatusNotImplemented, "unsupported", "provider editing is not available")
		return
	}
	if err := s.deps.DeleteProvider(c.Param("name")); err != nil {
		fail(c, http.StatusBadRequest, "provider_delete", err.Error())
		return
	}
	c.Status(http.StatusNoContent)
}

// setProviderKey stores the provider key.
func (s *Server) setProviderKey(c *gin.Context) {
	var body struct {
		Key string `json:"key"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, http.StatusBadRequest, "bad_params", err.Error())
		return
	}
	if s.deps.SetProviderKey == nil {
		fail(c, http.StatusNotImplemented, "unsupported", "key editing is not available")
		return
	}
	if err := s.deps.SetProviderKey(c.Param("name"), body.Key); err != nil {
		fail(c, http.StatusBadRequest, "key_save", err.Error())
		return
	}
	c.Status(http.StatusNoContent)
}

// testProvider checks the provider connection.
func (s *Server) testProvider(c *gin.Context) {
	if s.deps.TestProvider == nil {
		fail(c, http.StatusNotImplemented, "unsupported", "provider testing is not available")
		return
	}
	message, err := s.deps.TestProvider(c.Param("name"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"ok": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "message": message})
}

// saveModel creates or updates a model.
func (s *Server) saveModel(c *gin.Context) {
	var body ModelEdit
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, http.StatusBadRequest, "bad_params", err.Error())
		return
	}
	if body.Name == "" {
		fail(c, http.StatusBadRequest, "bad_params", "name is required")
		return
	}
	if body.Provider == "" {
		fail(c, http.StatusBadRequest, "bad_params", "provider is required")
		return
	}
	if s.deps.SaveModel == nil {
		fail(c, http.StatusNotImplemented, "unsupported", "model editing is not available")
		return
	}
	if err := s.deps.SaveModel(body); err != nil {
		fail(c, http.StatusBadRequest, "model_invalid", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"saved": true})
}

// deleteModel removes a model.
func (s *Server) deleteModel(c *gin.Context) {
	if s.deps.DeleteModel == nil {
		fail(c, http.StatusNotImplemented, "unsupported", "model editing is not available")
		return
	}
	if err := s.deps.DeleteModel(c.Param("name"), c.Param("model")); err != nil {
		fail(c, http.StatusBadRequest, "model_delete", err.Error())
		return
	}
	c.Status(http.StatusNoContent)
}

// discoverProviderModels returns the provider model list.
func (s *Server) discoverProviderModels(c *gin.Context) {
	if s.deps.DiscoverModels == nil {
		fail(c, http.StatusNotImplemented, "unsupported", "model discovery is not available")
		return
	}
	list, err := s.deps.DiscoverModels(c.Param("name"))
	if err != nil {
		fail(c, http.StatusBadGateway, "discovery_failed", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"models": list})
}

// listRunning returns the running requests.
func (s *Server) listRunning(c *gin.Context) {
	if s.deps.Running == nil {
		c.JSON(http.StatusOK, gin.H{"turns": []RunningView{}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"turns": s.deps.Running()})
}

// cancelRunning interrupts a running request.
func (s *Server) cancelRunning(c *gin.Context) {
	if s.deps.CancelRunning == nil {
		fail(c, http.StatusNotImplemented, "unsupported", "turn control is not available")
		return
	}
	if !s.deps.CancelRunning(c.Param("id")) {
		fail(c, http.StatusNotFound, "not_found", "no such running request")
		return
	}
	c.JSON(http.StatusOK, gin.H{"canceled": true})
}

// listPresets returns the agent presets.
func (s *Server) listPresets(c *gin.Context) {
	if s.deps.Presets == nil {
		c.JSON(http.StatusOK, gin.H{"presets": []PresetView{}})
		return
	}
	list := s.deps.Presets()
	c.JSON(http.StatusOK, gin.H{"presets": list})
}

// savePreset creates or updates a preset.
func (s *Server) savePreset(c *gin.Context) {
	var body PresetEdit
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, http.StatusBadRequest, "bad_params", err.Error())
		return
	}
	if body.ID == "" {
		fail(c, http.StatusBadRequest, "bad_params", "id is required")
		return
	}
	if s.deps.SavePreset == nil {
		fail(c, http.StatusNotImplemented, "unsupported", "preset editing is not available")
		return
	}
	if err := s.deps.SavePreset(body); err != nil {
		fail(c, http.StatusBadRequest, "preset_invalid", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"saved": true})
}

// deletePreset removes a preset.
func (s *Server) deletePreset(c *gin.Context) {
	if s.deps.DeletePreset == nil {
		fail(c, http.StatusNotImplemented, "unsupported", "preset editing is not available")
		return
	}
	if err := s.deps.DeletePreset(c.Param("id")); err != nil {
		fail(c, http.StatusBadRequest, "preset_delete", err.Error())
		return
	}
	c.Status(http.StatusNoContent)
}

// setActivePreset makes a preset active.
func (s *Server) setActivePreset(c *gin.Context) {
	if s.deps.SetActivePreset == nil {
		fail(c, http.StatusNotImplemented, "unsupported", "preset selection is not available")
		return
	}
	if err := s.deps.SetActivePreset(c.Param("id")); err != nil {
		fail(c, http.StatusBadRequest, "preset_select", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"active": c.Param("id")})
}

// setDefaultModel marks a model as the default.
func (s *Server) setDefaultModel(c *gin.Context) {
	if s.deps.SetDefaultModel == nil {
		fail(c, http.StatusNotImplemented, "unsupported", "model editing is not available")
		return
	}
	if err := s.deps.SetDefaultModel(c.Param("name"), c.Param("model")); err != nil {
		fail(c, http.StatusBadRequest, "model_default", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"saved": true})
}

// listPlugins returns the plugins.
func (s *Server) listPlugins(c *gin.Context) {
	if s.deps.Plugins == nil {
		c.JSON(http.StatusOK, gin.H{"plugins": []PluginView{}})
		return
	}
	list, err := s.deps.Plugins()
	if err != nil {
		fail(c, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"plugins": list})
}

// setPlugin enables or disables a plugin.
func (s *Server) setPlugin(c *gin.Context) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, http.StatusBadRequest, "bad_params", err.Error())
		return
	}
	if s.deps.SetPlugin == nil {
		fail(c, http.StatusNotImplemented, "unsupported", "plugin control is not available")
		return
	}
	if err := s.deps.SetPlugin(c.Param("name"), body.Enabled); err != nil {
		fail(c, http.StatusBadRequest, "plugin_update", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"saved": true})
}

// deletePlugin removes a plugin from disk.
func (s *Server) deletePlugin(c *gin.Context) {
	if s.deps.DeletePlugin == nil {
		fail(c, http.StatusNotImplemented, "unsupported", "plugin removal is not available")
		return
	}
	if err := s.deps.DeletePlugin(c.Param("name")); err != nil {
		fail(c, http.StatusBadRequest, "plugin_delete", err.Error())
		return
	}
	c.Status(http.StatusNoContent)
}

// listMCP returns the servers.
func (s *Server) listMCP(c *gin.Context) {
	if s.deps.MCPServers == nil {
		c.JSON(http.StatusOK, gin.H{"servers": []MCPView{}})
		return
	}
	list, err := s.deps.MCPServers()
	if err != nil {
		fail(c, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"servers": list})
}

// saveMCP creates or updates a server.
func (s *Server) saveMCP(c *gin.Context) {
	var body MCPEdit
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, http.StatusBadRequest, "bad_params", err.Error())
		return
	}
	if body.Name == "" {
		fail(c, http.StatusBadRequest, "bad_params", "name is required")
		return
	}
	if s.deps.SaveMCP == nil {
		fail(c, http.StatusNotImplemented, "unsupported", "mcp editing is not available")
		return
	}
	if err := s.deps.SaveMCP(body); err != nil {
		fail(c, http.StatusBadRequest, "mcp_invalid", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"saved": true})
}

// deleteMCP removes a server.
func (s *Server) deleteMCP(c *gin.Context) {
	if s.deps.DeleteMCP == nil {
		fail(c, http.StatusNotImplemented, "unsupported", "mcp editing is not available")
		return
	}
	if err := s.deps.DeleteMCP(c.Param("name")); err != nil {
		fail(c, http.StatusBadRequest, "mcp_delete", err.Error())
		return
	}
	c.Status(http.StatusNoContent)
}

// restartMCP restarts a server.
func (s *Server) restartMCP(c *gin.Context) {
	if s.deps.RestartMCP == nil {
		fail(c, http.StatusNotImplemented, "unsupported", "mcp control is not available")
		return
	}
	if err := s.deps.RestartMCP(c.Param("name")); err != nil {
		fail(c, http.StatusBadRequest, "mcp_restart", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"restarted": true})
}

// savePerms stores the permission levels.
func (s *Server) savePerms(c *gin.Context) {
	var body PermissionsView
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, http.StatusBadRequest, "bad_params", err.Error())
		return
	}
	if s.deps.SavePerms == nil {
		fail(c, http.StatusNotImplemented, "unsupported", "permission editing is not available")
		return
	}
	if err := s.deps.SavePerms(body); err != nil {
		fail(c, http.StatusBadRequest, "permissions_invalid", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"saved": true})
}

// getRules returns the danger rules.
func (s *Server) getRules(c *gin.Context) {
	if s.deps.DangerRules == nil {
		c.JSON(http.StatusOK, gin.H{"rules": []DangerRuleView{}})
		return
	}
	list, err := s.deps.DangerRules()
	if err != nil {
		fail(c, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"rules": list})
}

// saveRules replaces the danger rule list.
func (s *Server) saveRules(c *gin.Context) {
	var body struct {
		Rules []DangerRuleView `json:"rules"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, http.StatusBadRequest, "bad_params", err.Error())
		return
	}
	if s.deps.SaveRules == nil {
		fail(c, http.StatusNotImplemented, "unsupported", "rule editing is not available")
		return
	}
	if err := s.deps.SaveRules(body.Rules); err != nil {
		fail(c, http.StatusBadRequest, "rules_invalid", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"saved": true})
}

// installPlugin downloads and installs a plugin by URL.
func (s *Server) installPlugin(c *gin.Context) {
	var body struct {
		URL string `json:"url"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, http.StatusBadRequest, "bad_params", err.Error())
		return
	}
	if body.URL == "" {
		fail(c, http.StatusBadRequest, "bad_params", "url is required")
		return
	}
	if s.deps.InstallPlugin == nil {
		fail(c, http.StatusNotImplemented, "unsupported", "plugin install is not available")
		return
	}

	view, err := s.deps.InstallPlugin(body.URL)
	if err != nil {
		fail(c, http.StatusBadRequest, "plugin_install", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"plugin": view})
}

// NewProviderView builds a provider for the interface.
func NewProviderView(p store.Provider, hasKey bool, models []ModelView) ProviderView {
	return ProviderView{
		Name:     p.Name,
		Protocol: p.Protocol,
		BaseURL:  p.BaseURL,
		HasKey:   hasKey,
		Models:   models,
	}
}

// NewModelView builds a model for the interface.
func NewModelView(m store.Model) ModelView {
	return ModelView{
		Provider:       m.Provider,
		Name:           m.Name,
		Remote:         m.Remote,
		Default:        m.Default,
		ContextWindow:  m.ContextWindow,
		MaxTokens:      m.MaxTokens,
		Temperature:    m.Temperature,
		Reasoning:      m.Reasoning.LevelNames(),
		ReasoningMap:   m.Reasoning.Levels,
		ReasoningDef:   m.Reasoning.Default,
		AskReasoning:   m.Compat.SupportsReasoningEffort,
		MaxTokensField: m.Compat.MaxTokensField,
		SkipTemp:       m.Compat.SupportsTemperature,
	}
}
