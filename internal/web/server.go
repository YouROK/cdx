// Package web serves the REST API and the embedded interface.
package web

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/yourok/cdx/internal/config"
	"github.com/yourok/cdx/internal/i18n"
	"github.com/yourok/cdx/internal/webassets"
)

// tokenHeader carries the access token.
const tokenHeader = "X-CDX-Token"

// Server serves the web interface and the API.
type Server struct {
	cfg  *config.Config
	deps Deps

	mu    sync.RWMutex
	token string
}

// Deps are the dependencies the daemon provides.
type Deps struct {
	Status      func() StatusView
	Config      func() any
	SaveCfg     func(cfg map[string]any) (bool, error)
	Sessions    func() ([]SessionView, error)
	Session     func(id string) (SessionView, []MessageView, error)
	Prompt      func() (string, bool, error)
	SetPrompt   func(text string) error
	ClearPrompt func() error
	Logs        func(lines int) ([]string, error)
	Perms       func() PermissionsView
	// Language reports the program language to the interface.
	Language func() string

	// Providers and their models.
	Providers      func() ([]ProviderView, error)
	SaveProvider   func(ProviderEdit) error
	DeleteProvider func(name string) error
	SetProviderKey func(name, key string) error
	TestProvider   func(name string) (string, error)
	DiscoverModels func(name string) ([]DiscoveredModelView, error)

	// Provider models.
	SaveModel       func(ModelEdit) error
	DeleteModel     func(provider, name string) error
	SetDefaultModel func(provider, name string) error

	// Running requests.
	Running       func() []RunningView
	CancelRunning func(id string) bool

	// Agent presets.
	Presets         func() []PresetView
	SavePreset      func(PresetEdit) error
	DeletePreset    func(id string) error
	SetActivePreset func(id string) error

	// Plugins.
	Plugins       func() ([]PluginView, error)
	SetPlugin     func(name string, enabled bool) error
	DeletePlugin  func(name string) error
	InstallPlugin func(url string) (PluginView, error)

	// MCP servers.
	MCPServers func() ([]MCPView, error)
	SaveMCP    func(MCPEdit) error
	DeleteMCP  func(name string) error
	RestartMCP func(name string) error

	// Permissions and danger rules.
	SavePerms   func(PermissionsView) error
	DangerRules func() ([]DangerRuleView, error)
	SaveRules   func([]DangerRuleView) error
}

// New creates a server.
func New(cfg *config.Config, deps Deps) *Server {
	s := &Server{cfg: cfg, deps: deps}
	s.token = newToken()
	return s
}

// newToken generates a random access token.
func newToken() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return hex.EncodeToString([]byte{0})
	}
	return hex.EncodeToString(buf)
}

// Token returns the access token.
func (s *Server) Token() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.token
}

// URL returns the interface address with the token.
func (s *Server) URL() string {
	return "http://" + s.cfg.Web.Addr + ":" + itoa(s.cfg.Web.Port) + "/#/?token=" + s.Token()
}

// Handler builds the routes.
func (s *Server) Handler() http.Handler {
	gin.SetMode(gin.ReleaseMode)

	r := gin.New()
	r.Use(gin.Recovery())
	// Without this Gin answers 301 on paths with a trailing slash, which
	// an SPA does not expect.
	r.RedirectTrailingSlash = false

	api := r.Group("/api", s.auth())
	{
		api.GET("/status", s.status)
		api.GET("/config", s.getConfig)
		api.PUT("/config", s.putConfig)
		api.GET("/models", s.listModels)
		api.GET("/providers", s.listProviders)
		api.POST("/providers", s.saveProvider)
		api.PUT("/providers/:name", s.saveProvider)
		api.DELETE("/providers/:name", s.deleteProvider)
		api.PUT("/providers/:name/key", s.setProviderKey)
		api.POST("/providers/:name/test", s.testProvider)
		api.GET("/providers/:name/models/discover", s.discoverProviderModels)
		api.POST("/models", s.saveModel)
		api.DELETE("/providers/:name/models/:model", s.deleteModel)
		api.PUT("/providers/:name/models/:model/default", s.setDefaultModel)
		api.GET("/running", s.listRunning)
		api.POST("/running/:id/cancel", s.cancelRunning)
		api.GET("/presets", s.listPresets)
		api.POST("/presets", s.savePreset)
		api.DELETE("/presets/:id", s.deletePreset)
		api.PUT("/presets/:id/active", s.setActivePreset)
		api.GET("/plugins", s.listPlugins)
		api.POST("/plugins", s.installPlugin)
		api.PUT("/plugins/:name", s.setPlugin)
		api.DELETE("/plugins/:name", s.deletePlugin)
		api.GET("/mcp", s.listMCP)
		api.POST("/mcp", s.saveMCP)
		api.PUT("/mcp/:name", s.saveMCP)
		api.DELETE("/mcp/:name", s.deleteMCP)
		api.POST("/mcp/:name/restart", s.restartMCP)
		api.PUT("/permissions", s.savePerms)
		api.GET("/dangerous-rules", s.getRules)
		api.PUT("/dangerous-rules", s.saveRules)
		api.GET("/sessions", s.listSessions)
		api.GET("/sessions/:id", s.showSession)
		api.GET("/prompt", s.getPrompt)
		api.PUT("/prompt", s.putPrompt)
		api.DELETE("/prompt", s.deletePrompt)
		api.GET("/logs", s.getLogs)
		api.GET("/permissions", s.getPermissions)
	}

	s.serveStatic(r)
	return r
}

// serveIndex serves index.html with caching disabled.
func (s *Server) serveIndex(c *gin.Context) {
	if !webassets.Available() {
		c.String(http.StatusOK, "interface build is missing")
		return
	}

	data, _, ok := readAsset(webassets.FS(), "/index.html")
	if !ok {
		c.String(http.StatusInternalServerError, "interface build is corrupted")
		return
	}

	// The token is embedded in the page, so the interface does not depend on
	// the URL fragment arriving and survives a tab reload.
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "text/html; charset=utf-8", injectToken(data, s.Token()))
}

// tokenPlaceholder is the token slot in index.html.
const tokenPlaceholder = "<!--cdx-token-->"

// injectToken puts the token into the page.
func injectToken(page []byte, token string) []byte {
	tag := []byte("<script>window.__CDX_TOKEN__=" + strconv.Quote(token) + "</script>")
	out := bytes.Replace(page, []byte(tokenPlaceholder), tag, 1)
	if len(out) == len(page) {
		// No placeholder, yet the interface still needs the token.
		return page
	}
	return out
}

// auth checks the token for read and write requests.
//
// Being local is no protection: any page open in a browser can reach
// 127.0.0.1. The token is therefore required on every route.
func (s *Server) auth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetHeader(tokenHeader) != s.Token() {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code":    "unauthorized",
				"message": "token is missing or invalid",
			})
			return
		}
		c.Next()
	}
}

// serveStatic serves the built interface and falls back to index.html.
func (s *Server) serveStatic(r *gin.Engine) {
	fs := webassets.FS()
	if !webassets.Available() || fs == nil {
		r.NoRoute(func(c *gin.Context) {
			if strings.HasPrefix(c.Request.URL.Path, "/api") {
				c.JSON(http.StatusNotFound, gin.H{
					"code":    "not_found",
					"message": "unknown method",
				})
				return
			}
			c.String(http.StatusOK, "interface build is missing")
		})
		return
	}

	// The root has an explicit route: NoRoute is not called for it.
	r.GET("/", s.serveIndex)

	r.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path
		if strings.HasPrefix(path, "/api") {
			c.JSON(http.StatusNotFound, gin.H{
				"code":    "not_found",
				"message": "unknown method",
			})
			return
		}

		// The file is written directly: http.FileServer redirects to "/"
		// and breaks the app root.
		if data, contentType, ok := readAsset(fs, path); ok {
			if isHashed(path) {
				c.Header("Cache-Control", "public, max-age=31536000, immutable")
			}
			c.Data(http.StatusOK, contentType, data)
			return
		}

		s.serveIndex(c)
	})
}

// isHashed reports whether the file sits in a content hashed directory.
func isHashed(path string) bool {
	return strings.Contains(path, "/assets/")
}

// fail writes an error in the shared format.
func fail(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{"code": code, "message": message})
}

// status returns the daemon state.
func (s *Server) status(c *gin.Context) {
	view := s.deps.Status()
	view.Language = i18n.Language()
	if s.deps.Language != nil {
		view.Language = s.deps.Language()
	}
	c.JSON(http.StatusOK, view)
}

// getConfig returns the editable part of the config.
func (s *Server) getConfig(c *gin.Context) {
	c.JSON(http.StatusOK, s.deps.Config())
}

// putConfig stores the config.
func (s *Server) putConfig(c *gin.Context) {
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, http.StatusBadRequest, "bad_params", err.Error())
		return
	}

	restart, err := s.deps.SaveCfg(body)
	if err != nil {
		fail(c, http.StatusBadRequest, "config_invalid", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"saved": true, "restart_required": restart})
}

// listModels returns models of every provider.
func (s *Server) listModels(c *gin.Context) {
	if s.deps.Providers == nil {
		c.JSON(http.StatusOK, gin.H{"models": []ModelView{}})
		return
	}
	providers, err := s.deps.Providers()
	if err != nil {
		fail(c, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	out := make([]ModelView, 0)
	for _, p := range providers {
		out = append(out, p.Models...)
	}
	c.JSON(http.StatusOK, gin.H{"models": out})
}

// listSessions returns the sessions.
func (s *Server) listSessions(c *gin.Context) {
	sessions, err := s.deps.Sessions()
	if err != nil {
		fail(c, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"sessions": sessions})
}

// showSession returns a session history.
func (s *Server) showSession(c *gin.Context) {
	view, messages, err := s.deps.Session(c.Param("id"))
	if err != nil {
		fail(c, http.StatusNotFound, "not_found", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"session": view, "messages": messages})
}

// getPrompt returns the system prompt.
func (s *Server) getPrompt(c *gin.Context) {
	text, isDefault, err := s.deps.Prompt()
	if err != nil {
		fail(c, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"text": text, "is_default": isDefault})
}

// putPrompt stores the system prompt.
func (s *Server) putPrompt(c *gin.Context) {
	var body struct {
		Text string `json:"text"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, http.StatusBadRequest, "bad_params", err.Error())
		return
	}
	if strings.TrimSpace(body.Text) == "" {
		fail(c, http.StatusBadRequest, "prompt_empty",
			"prompt must not be empty, use reset to restore the default")
		return
	}
	if err := s.deps.SetPrompt(body.Text); err != nil {
		fail(c, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	c.Status(http.StatusNoContent)
}

// deletePrompt resets the prompt to the built-in one.
func (s *Server) deletePrompt(c *gin.Context) {
	if err := s.deps.ClearPrompt(); err != nil {
		fail(c, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	c.Status(http.StatusNoContent)
}

// getLogs returns the log tail.
func (s *Server) getLogs(c *gin.Context) {
	lines := 200
	if raw := c.Query("tail"); raw != "" {
		if parsed, err := atoi(raw); err == nil && parsed > 0 && parsed <= 2000 {
			lines = parsed
		}
	}

	out, err := s.deps.Logs(lines)
	if err != nil {
		fail(c, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"lines": out})
}

// getPermissions returns the active permissions.
func (s *Server) getPermissions(c *gin.Context) {
	c.JSON(http.StatusOK, s.deps.Perms())
}

// StatusView is the daemon state for the interface.
type StatusView struct {
	State    string `json:"state"`
	Version  string `json:"version"`
	Proto    int    `json:"proto"`
	PID      int    `json:"pid"`
	Uptime   int64  `json:"uptime_s"`
	Language string `json:"language"`
}

// SessionView is a session for the interface.
type SessionView struct {
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

// MessageView is a history entry for the interface.
type MessageView struct {
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

// PermissionsView holds permissions for the interface.
type PermissionsView struct {
	Read           string `json:"read"`
	Edit           string `json:"edit"`
	Dangerous      string `json:"dangerous"`
	NonInteractive string `json:"non_interactive"`
}

// TailLogs reads the last lines of the log file.
func TailLogs(dir string, lines int) ([]string, error) {
	path := filepath.Join(dir, "cdx.log")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}

	all := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if lines > 0 && len(all) > lines {
		all = all[len(all)-lines:]
	}
	return all, nil
}

// itoa converts a number to a string.
func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// atoi parses an integer.
func atoi(s string) (int, error) {
	out := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, errBadNumber
		}
		out = out*10 + int(r-'0')
	}
	return out, nil
}

// errBadNumber reports an invalid number.
var errBadNumber = os.ErrInvalid

// readAsset reads a static file and detects its content type.
func readAsset(fs http.FileSystem, path string) ([]byte, string, bool) {
	if path == "" || path == "/" {
		path = "/index.html"
	}

	f, err := fs.Open(path)
	if err != nil {
		return nil, "", false
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil || st.IsDir() {
		return nil, "", false
	}

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, "", false
	}

	ext := strings.ToLower(filepath.Ext(path))
	contentType, ok := contentTypes[ext]
	if !ok {
		contentType = "application/octet-stream"
	}
	return data, contentType, true
}

// contentTypes maps build file extensions to types.
var contentTypes = map[string]string{
	".html":  "text/html; charset=utf-8",
	".js":    "text/javascript; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".json":  "application/json",
	".svg":   "image/svg+xml",
	".png":   "image/png",
	".jpg":   "image/jpeg",
	".ico":   "image/x-icon",
	".woff":  "font/woff",
	".woff2": "font/woff2",
	".map":   "application/json",
}
