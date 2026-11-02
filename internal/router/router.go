// Package router provides a dynamic Gin-backed HTTP router where plugins can register, override, and unload routes, middleware, static assets, and UI descriptors.
package router

import (
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.ReleaseMode)
}

// HandlerFunc is a Gin handler registered by the core or a plugin.
type HandlerFunc = gin.HandlerFunc

// Context is the Gin request context.
type Context = gin.Context

// UIDescriptor describes a plugin's panel in the web interface.
type UIDescriptor struct {
	PluginID    string         `json:"plugin_id"`
	Title       string         `json:"title"`
	Description string         `json:"description,omitempty"`
	Icon        string         `json:"icon,omitempty"`
	Order       int            `json:"order,omitempty"`
	Entry       string         `json:"entry,omitempty"`
	Schema      map[string]any `json:"schema,omitempty"`
}

type routeEntry struct {
	owner   string
	method  string
	pattern string
	handler HandlerFunc
}

type middlewareEntry struct {
	owner   string
	prefix  string
	handler HandlerFunc
}

type staticEntry struct {
	owner  string
	prefix string
	dir    string
}

// Router manages dynamic HTTP routes, middleware, static mounts, and UI descriptors on top of Gin.
type Router struct {
	mu          sync.RWMutex
	routes      []routeEntry
	middlewares []middlewareEntry
	statics     []staticEntry
	uis         map[string]UIDescriptor
	engine      *gin.Engine
}

// New creates a new dynamic Router instance.
func New() *Router {
	r := &Router{
		uis: make(map[string]UIDescriptor),
	}
	r.rebuildLocked()
	return r
}

// ServeHTTP dispatches incoming HTTP requests to the current compiled Gin engine.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mu.RLock()
	eng := r.engine
	r.mu.RUnlock()
	eng.ServeHTTP(w, req)
}

// Handle registers or overrides an HTTP route for the given owner (e.g. "core" or "plugin:webui").
func (r *Router) Handle(owner, method, pattern string, handler HandlerFunc) {
	method = strings.ToUpper(strings.TrimSpace(method))
	pattern = normalizePattern(pattern)

	r.mu.Lock()
	defer r.mu.Unlock()

	for i := range r.routes {
		if r.routes[i].method == method && r.routes[i].pattern == pattern {
			r.routes[i].owner = owner
			r.routes[i].handler = handler
			r.rebuildLocked()
			return
		}
	}

	r.routes = append(r.routes, routeEntry{
		owner:   owner,
		method:  method,
		pattern: pattern,
		handler: handler,
	})
	r.rebuildLocked()
}

// Use registers a middleware for all paths or a specific path prefix.
func (r *Router) Use(owner, prefix string, handler HandlerFunc) {
	prefix = normalizePrefix(prefix)

	r.mu.Lock()
	defer r.mu.Unlock()

	r.middlewares = append(r.middlewares, middlewareEntry{
		owner:   owner,
		prefix:  prefix,
		handler: handler,
	})
	r.rebuildLocked()
}

// Static mounts a filesystem directory under a URL prefix for the given owner.
func (r *Router) Static(owner, prefix, dir string) {
	prefix = normalizePrefix(prefix)
	dir = filepath.Clean(dir)

	r.mu.Lock()
	defer r.mu.Unlock()

	for i := range r.statics {
		if r.statics[i].prefix == prefix {
			r.statics[i].owner = owner
			r.statics[i].dir = dir
			r.rebuildLocked()
			return
		}
	}

	r.statics = append(r.statics, staticEntry{
		owner:  owner,
		prefix: prefix,
		dir:    dir,
	})
	r.rebuildLocked()
}

// RegisterUI registers or updates a plugin's UI panel descriptor.
func (r *Router) RegisterUI(desc UIDescriptor) {
	if desc.PluginID == "" {
		return
	}
	r.mu.Lock()
	r.uis[desc.PluginID] = desc
	r.mu.Unlock()
}

// ListUI returns all registered plugin UI descriptors sorted by Order then PluginID.
func (r *Router) ListUI() []UIDescriptor {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]UIDescriptor, 0, len(r.uis))
	for _, d := range r.uis {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Order != out[j].Order {
			return out[i].Order < out[j].Order
		}
		return out[i].PluginID < out[j].PluginID
	})
	return out
}

// UnregisterOwner removes all routes, middlewares, static mounts, and UI descriptors belonging to owner.
func (r *Router) UnregisterOwner(owner string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	routes := r.routes[:0]
	for _, rt := range r.routes {
		if rt.owner != owner {
			routes = append(routes, rt)
		}
	}
	r.routes = routes

	mws := r.middlewares[:0]
	for _, mw := range r.middlewares {
		if mw.owner != owner {
			mws = append(mws, mw)
		}
	}
	r.middlewares = mws

	statics := r.statics[:0]
	for _, st := range r.statics {
		if st.owner != owner {
			statics = append(statics, st)
		}
	}
	r.statics = statics

	pluginID := strings.TrimPrefix(owner, "plugin:")
	delete(r.uis, pluginID)
	delete(r.uis, owner)

	r.rebuildLocked()
}

func (r *Router) rebuildLocked() {
	eng := gin.New()
	eng.Use(gin.Recovery())

	// Attach prefix-scoped or global plugin middlewares.
	for _, mw := range r.middlewares {
		entry := mw
		eng.Use(func(c *gin.Context) {
			if matchesPrefix(c.Request.URL.Path, entry.prefix) {
				entry.handler(c)
			} else {
				c.Next()
			}
		})
	}

	// Register static directories.
	for _, st := range r.statics {
		eng.StaticFS(st.prefix, gin.Dir(st.dir, false))
	}

	// Register explicit routes.
	for _, rt := range r.routes {
		if rt.method == "ANY" {
			eng.Any(rt.pattern, rt.handler)
		} else {
			eng.Handle(rt.method, rt.pattern, rt.handler)
		}
	}

	r.engine = eng
}

func normalizePattern(p string) string {
	p = strings.TrimSpace(p)
	if p == "" || !strings.HasPrefix(p, "/") {
		return "/" + p
	}
	return p
}

func normalizePrefix(p string) string {
	p = strings.TrimSpace(p)
	p = strings.TrimSuffix(p, "*")
	p = strings.TrimSuffix(p, "/")
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		return "/" + p
	}
	return p
}

func matchesPrefix(reqPath, prefix string) bool {
	if prefix == "" || prefix == "/" {
		return true
	}
	return reqPath == prefix || strings.HasPrefix(reqPath, prefix+"/")
}
