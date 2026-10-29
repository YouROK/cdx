package pluginapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yourok/cdx/internal/config"
	"github.com/yourok/cdx/internal/perm"
)

// TestExec covers running a command.
func TestExec(t *testing.T) {
	api, _ := newAPI(t)

	res, err := api.Exec("echo привет")
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if strings.TrimSpace(res.Stdout) != "привет" {
		t.Fatalf("unexpected stdout %q", res.Stdout)
	}
	if res.Code != 0 {
		t.Fatalf("unexpected code %d", res.Code)
	}
}

// TestExecNonZero covers a non-zero code being a result, not a failure.
func TestExecNonZero(t *testing.T) {
	api, _ := newAPI(t)

	res, err := api.Exec("exit 3")
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if res.Code != 3 {
		t.Fatalf("expected code 3, got %d", res.Code)
	}
}

// TestExecStderr covers collecting the command errors.
func TestExecStderr(t *testing.T) {
	api, _ := newAPI(t)

	res, err := api.Exec("echo ошибка >&2")
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if !strings.Contains(res.Stderr, "ошибка") {
		t.Fatalf("stderr is lost: %+v", res)
	}
}

// TestExecTruncates covers cutting the output.
func TestExecTruncates(t *testing.T) {
	api, _ := newAPI(t)
	api.limits.MaxOutputBytes = 10

	res, err := api.Exec("printf 'aaaaaaaaaaaaaaaaaaaa'")
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if !res.Truncated {
		t.Fatal("output must be marked as truncated")
	}
	if len(res.Stdout) > 10 {
		t.Fatalf("output exceeds the limit: %d", len(res.Stdout))
	}
}

// TestExecTimeout covers the time limit.
func TestExecTimeout(t *testing.T) {
	api, _ := newAPI(t)
	api.limits.TimeoutMS = 100

	res, err := api.Exec("sleep 5")
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if !res.TimedOut {
		t.Fatal("command must be stopped by the timeout")
	}
}

// TestExecDenied covers a refusal without the edit right.
func TestExecDenied(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Perms.Edit = "deny"
	pm, err := perm.New(cfg)
	if err != nil {
		t.Fatalf("perm: %v", err)
	}
	api := New(Options{Perms: pm, Root: dir})

	if _, err := api.Exec("echo hi"); err == nil {
		t.Fatal("shell must be denied when edit is denied")
	}
}

// TestWriteDenied covers a refusal to write without the right.
func TestWriteDenied(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Perms.Edit = "deny"
	pm, _ := perm.New(cfg)
	api := New(Options{Perms: pm, Root: dir})

	if _, err := api.Write("a.txt", "x"); err == nil {
		t.Fatal("write must be denied when edit is denied")
	}
}

// TestHTTPRequest covers a network request.
func TestHTTPRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test") != "yes" {
			t.Errorf("header is lost")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	api, _ := newAPI(t)
	res, err := api.HTTP(HTTPRequest{
		URL:     srv.URL,
		Method:  "GET",
		Headers: map[string]string{"X-Test": "yes"},
	})
	if err != nil {
		t.Fatalf("HTTP: %v", err)
	}
	if res.Status != 200 || !strings.Contains(res.Body, "ok") {
		t.Fatalf("unexpected response %+v", res)
	}
	if res.Headers["content-type"] != "application/json" {
		t.Fatalf("headers are lost: %+v", res.Headers)
	}
}

// TestHTTPPost covers the request body.
func TestHTTPPost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		buf := make([]byte, 64)
		n, _ := r.Body.Read(buf)
		_, _ = w.Write(buf[:n])
	}))
	defer srv.Close()

	api, _ := newAPI(t)
	res, err := api.HTTP(HTTPRequest{URL: srv.URL, Method: "POST", Body: "данные"})
	if err != nil {
		t.Fatalf("HTTP: %v", err)
	}
	if res.Body != "данные" {
		t.Fatalf("body is lost: %q", res.Body)
	}
}

// TestHTTPRejectsBadScheme covers the non-HTTP scheme ban.
func TestHTTPRejectsBadScheme(t *testing.T) {
	api, _ := newAPI(t)

	for _, url := range []string{"file:///etc/passwd", "ftp://example.com/x", "not-a-url"} {
		if _, err := api.HTTP(HTTPRequest{URL: url}); err == nil {
			t.Fatalf("url %q must be rejected", url)
		}
	}
}

// TestHTTPRejectsBadMethod covers an unsupported method.
func TestHTTPRejectsBadMethod(t *testing.T) {
	api, _ := newAPI(t)
	if _, err := api.HTTP(HTTPRequest{URL: "https://example.com", Method: "TRACE"}); err == nil {
		t.Fatal("unsupported method must be rejected")
	}
}

// TestHTTPTruncates covers cutting a large reply.
func TestHTTPTruncates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 1000)))
	}))
	defer srv.Close()

	api, _ := newAPI(t)
	api.limits.MaxOutputBytes = 50

	res, err := api.HTTP(HTTPRequest{URL: srv.URL})
	if err != nil {
		t.Fatalf("HTTP: %v", err)
	}
	if !res.Truncated || len(res.Body) > 50 {
		t.Fatalf("response must be truncated: len=%d", len(res.Body))
	}
}
