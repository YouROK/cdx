package router

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRoutesOverrideAndUnregister(t *testing.T) {
	r := New()

	r.Handle("plugin:a", "GET", "/hello/:name", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"from": "a", "name": c.Param("name")})
	})

	req := httptest.NewRequest("GET", "/hello/world", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK || w.Body.String() != `{"from":"a","name":"world"}` {
		t.Fatalf("unexpected response from plugin:a: %d %s", w.Code, w.Body.String())
	}

	// Override the same route from plugin:b.
	r.Handle("plugin:b", "GET", "/hello/:name", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"from": "b", "name": c.Param("name")})
	})

	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req)
	if w2.Code != http.StatusOK || w2.Body.String() != `{"from":"b","name":"world"}` {
		t.Fatalf("unexpected response after override: %d %s", w2.Code, w2.Body.String())
	}

	// Unregister plugin:b and verify 404.
	r.UnregisterOwner("plugin:b")
	w3 := httptest.NewRecorder()
	r.ServeHTTP(w3, req)
	if w3.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after unregister, got %d", w3.Code)
	}
}

func TestMiddlewareContextAndAbort(t *testing.T) {
	r := New()

	r.Use("plugin:auth", "/api", func(c *gin.Context) {
		if c.GetHeader("X-Token") != "secret" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		c.Set("user", "admin")
		c.Next()
	})

	r.Handle("plugin:providers", "GET", "/api/models", func(c *gin.Context) {
		user, _ := c.Get("user")
		c.JSON(http.StatusOK, gin.H{"user": user})
	})

	r.Handle("plugin:webui", "GET", "/public", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	// Unauthorized request to /api/models.
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, httptest.NewRequest("GET", "/api/models", nil))
	if w1.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w1.Code)
	}

	// Authorized request to /api/models.
	req2 := httptest.NewRequest("GET", "/api/models", nil)
	req2.Header.Set("X-Token", "secret")
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK || w2.Body.String() != `{"user":"admin"}` {
		t.Fatalf("unexpected authorized response: %d %s", w2.Code, w2.Body.String())
	}

	// Public route outside /api prefix should bypass auth middleware.
	w3 := httptest.NewRecorder()
	r.ServeHTTP(w3, httptest.NewRequest("GET", "/public", nil))
	if w3.Code != http.StatusOK || w3.Body.String() != "ok" {
		t.Fatalf("unexpected public response: %d %s", w3.Code, w3.Body.String())
	}
}

func TestStaticAndUIDescriptors(t *testing.T) {
	r := New()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.js"), []byte("console.log(1);"), 0o600); err != nil {
		t.Fatalf("write static file: %v", err)
	}

	r.Static("plugin:webui", "/assets", dir)
	r.RegisterUI(UIDescriptor{PluginID: "providers", Title: "Providers", Order: 20})
	r.RegisterUI(UIDescriptor{PluginID: "general", Title: "General", Order: 10})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/assets/app.js", nil))
	if w.Code != http.StatusOK || w.Body.String() != "console.log(1);" {
		t.Fatalf("unexpected static response: %d %s", w.Code, w.Body.String())
	}
	if cache := w.Header().Get("Cache-Control"); cache != "no-cache" {
		t.Fatalf("static assets must be revalidated, got Cache-Control %q", cache)
	}

	uis := r.ListUI()
	if len(uis) != 2 || uis[0].PluginID != "general" || uis[1].PluginID != "providers" {
		t.Fatalf("unexpected UI descriptor order: %+v", uis)
	}
}
