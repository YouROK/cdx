package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yourok/cdx/internal/config"
	"github.com/yourok/cdx/internal/webassets"
)

// newServer builds a server with stubbed dependencies.
func newServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()

	deps := Deps{
		Status: func() StatusView {
			return StatusView{State: "running", Version: "test", Proto: 1, PID: 42, Uptime: 7}
		},
		Config: func() any { return map[string]any{"agent": map[string]any{"max_iterations": 50}} },
		SaveCfg: func(body map[string]any) (bool, error) {
			if _, ok := body["reject"]; ok {
				return false, errors.New("rejected")
			}
			return true, nil
		},
		Providers: func() ([]ProviderView, error) {
			return []ProviderView{{
				Name: "p", BaseURL: "http://x", HasKey: true,
				Models: []ModelView{{Provider: "p", Name: "m", Remote: "remote"}},
			}}, nil
		},
		Sessions: func() ([]SessionView, error) {
			return []SessionView{{ID: "s1", Title: "тест"}}, nil
		},
		Session: func(id string) (SessionView, []MessageView, error) {
			if id != "s1" {
				return SessionView{}, nil, errors.New("not found")
			}
			return SessionView{ID: "s1"}, []MessageView{{Seq: 1, Kind: "user", Content: "привет"}}, nil
		},
		Prompt: func() (string, bool, error) { return "text", false, nil },
		SetPrompt: func(text string) error {
			if text == "fail" {
				return errors.New("store failed")
			}
			return nil
		},
		ClearPrompt: func() error { return nil },
		Logs: func(lines int) ([]string, error) {
			return []string{"line1", "line2"}, nil
		},
		Perms: func() PermissionsView {
			return PermissionsView{Read: "allow", Edit: "ask", Dangerous: "ask", NonInteractive: "deny"}
		},
		Language: func() string { return "ru" },
	}

	s := New(config.Default(), deps)
	return s, s.Handler()
}

// do sends a request to the test server.
func do(t *testing.T, h http.Handler, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()

	var reader *strings.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		reader = strings.NewReader(string(data))
	} else {
		reader = strings.NewReader("")
	}

	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set(tokenHeader, token)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestAuthRequired checks the token is required.
func TestAuthRequired(t *testing.T) {
	s, h := newServer(t)

	rec := do(t, h, http.MethodGet, "/api/status", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", rec.Code)
	}

	rec = do(t, h, http.MethodGet, "/api/status", "wrong", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with wrong token, got %d", rec.Code)
	}

	rec = do(t, h, http.MethodGet, "/api/status", s.Token(), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with valid token, got %d", rec.Code)
	}
}

// TestStatusIncludesLanguage checks the language in the status answer.
func TestStatusIncludesLanguage(t *testing.T) {
	s, h := newServer(t)

	rec := do(t, h, http.MethodGet, "/api/status", s.Token(), nil)
	var body StatusView
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Language != "ru" {
		t.Fatalf("expected language ru, got %q", body.Language)
	}
	if body.State != "running" || body.PID != 42 {
		t.Fatalf("unexpected status %+v", body)
	}
}

// TestUnknownAPIReturns404 checks the answer for an unknown method.
func TestUnknownAPIReturns404(t *testing.T) {
	s, h := newServer(t)

	rec := do(t, h, http.MethodGet, "/api/nope", s.Token(), nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unknown API must return JSON: %v", err)
	}
	if body["code"] != "not_found" {
		t.Fatalf("unexpected code %v", body["code"])
	}
}

// TestPutConfigRejectsInvalid checks a save error.
func TestPutConfigRejectsInvalid(t *testing.T) {
	s, h := newServer(t)

	rec := do(t, h, http.MethodPut, "/api/config", s.Token(), map[string]any{"reject": true})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

// TestPutConfigReportsRestart checks the restart flag.
func TestPutConfigReportsRestart(t *testing.T) {
	s, h := newServer(t)

	rec := do(t, h, http.MethodPut, "/api/config", s.Token(), map[string]any{"agent": map[string]any{}})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var body struct {
		Saved   bool `json:"saved"`
		Restart bool `json:"restart_required"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Saved || !body.Restart {
		t.Fatalf("unexpected body %+v", body)
	}
}

// TestModelsHidesKeys checks no keys leak into the answer.
func TestModelsHidesKeys(t *testing.T) {
	s, h := newServer(t)

	rec := do(t, h, http.MethodGet, "/api/providers", s.Token(), nil)
	payload := rec.Body.String()
	if strings.Contains(payload, "api_key") || strings.Contains(payload, "secret") {
		t.Fatalf("providers response must not contain keys: %s", payload)
	}
	if !strings.Contains(payload, `"has_key":true`) {
		t.Fatalf("expected has_key flag, got %s", payload)
	}
}

// TestSessionMissing checks 404 for an unknown session.
func TestSessionMissing(t *testing.T) {
	s, h := newServer(t)

	rec := do(t, h, http.MethodGet, "/api/sessions/unknown", s.Token(), nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

// TestSessionFound checks the history is returned.
func TestSessionFound(t *testing.T) {
	s, h := newServer(t)

	rec := do(t, h, http.MethodGet, "/api/sessions/s1", s.Token(), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var body struct {
		Session  SessionView   `json:"session"`
		Messages []MessageView `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Session.ID != "s1" || len(body.Messages) != 1 {
		t.Fatalf("unexpected body %+v", body)
	}
}

// TestPutPromptRejectsEmpty checks an empty prompt is refused.
func TestPutPromptRejectsEmpty(t *testing.T) {
	s, h := newServer(t)

	rec := do(t, h, http.MethodPut, "/api/prompt", s.Token(), map[string]any{"text": "   "})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

// TestPutPromptSaves checks the prompt is saved.
func TestPutPromptSaves(t *testing.T) {
	s, h := newServer(t)

	rec := do(t, h, http.MethodPut, "/api/prompt", s.Token(), map[string]any{"text": "новый"})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
}

// TestPutPromptStoreError checks a storage error.
func TestPutPromptStoreError(t *testing.T) {
	s, h := newServer(t)

	rec := do(t, h, http.MethodPut, "/api/prompt", s.Token(), map[string]any{"text": "fail"})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

// TestDeletePrompt checks the prompt reset.
func TestDeletePrompt(t *testing.T) {
	s, h := newServer(t)

	rec := do(t, h, http.MethodDelete, "/api/prompt", s.Token(), nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
}

// TestLogsTailClamped checks the tail parameter is parsed.
func TestLogsTailClamped(t *testing.T) {
	var seen int
	deps := Deps{Logs: func(lines int) ([]string, error) {
		seen = lines
		return nil, nil
	}}
	s := New(config.Default(), deps)
	h := s.Handler()

	// An invalid value must not break the request.
	rec := do(t, h, http.MethodGet, "/api/logs?tail=abc", s.Token(), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if seen != 200 {
		t.Fatalf("expected default 200, got %d", seen)
	}

	do(t, h, http.MethodGet, "/api/logs?tail=50", s.Token(), nil)
	if seen != 50 {
		t.Fatalf("expected 50, got %d", seen)
	}

	// An oversized value is clamped.
	do(t, h, http.MethodGet, "/api/logs?tail=99999", s.Token(), nil)
	if seen != 200 {
		t.Fatalf("expected clamp to default, got %d", seen)
	}
}

// TestTokenIsRandom checks tokens are unique.
func TestTokenIsRandom(t *testing.T) {
	a := New(config.Default(), Deps{})
	b := New(config.Default(), Deps{})
	if a.Token() == b.Token() {
		t.Fatal("tokens of two servers must differ")
	}
	if len(a.Token()) < 32 {
		t.Fatalf("token is too short: %q", a.Token())
	}
}

// TestURLContainsToken checks the interface address.
func TestURLContainsToken(t *testing.T) {
	s := New(config.Default(), Deps{})
	if !strings.Contains(s.URL(), "token="+s.Token()) {
		t.Fatalf("URL must contain the token: %s", s.URL())
	}
}

// TestTailLogs checks the log tail is read.
func TestTailLogs(t *testing.T) {
	dir := t.TempDir()

	lines, err := TailLogs(dir, 10)
	if err != nil {
		t.Fatalf("TailLogs on empty dir: %v", err)
	}
	if len(lines) != 0 {
		t.Fatalf("expected no lines, got %v", lines)
	}

	content := "one\ntwo\nthree\nfour\n"
	if err := os.WriteFile(filepath.Join(dir, "cdx.log"), []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	lines, err = TailLogs(dir, 2)
	if err != nil {
		t.Fatalf("TailLogs: %v", err)
	}
	if len(lines) != 2 || lines[0] != "three" || lines[1] != "four" {
		t.Fatalf("unexpected tail %v", lines)
	}

	lines, err = TailLogs(dir, 100)
	if err != nil {
		t.Fatalf("TailLogs: %v", err)
	}
	if len(lines) != 4 {
		t.Fatalf("expected all lines, got %v", lines)
	}
}

// TestStaticServed checks the interface is served.
func TestStaticServed(t *testing.T) {
	s := New(config.Default(), Deps{})
	h := s.Handler()

	rec := do(t, h, http.MethodGet, "/", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !webassets.Available() {
		if !strings.Contains(body, "interface build is missing") {
			t.Fatalf("unexpected body %q", body)
		}
		return
	}
	if !strings.Contains(body, "<div id=\"root\">") {
		t.Fatalf("index.html must be served, got %q", body)
	}
}

// TestStaticSPAFallback checks client routes fall back.
func TestStaticSPAFallback(t *testing.T) {
	if !webassets.Available() {
		t.Skip("interface is not built")
	}

	s := New(config.Default(), Deps{})
	h := s.Handler()

	rec := do(t, h, http.MethodGet, "/sessions", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for SPA route, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "<div id=\"root\">") {
		t.Fatalf("SPA route must serve index.html, got %q", rec.Body.String())
	}
}

// TestStaticCacheHeaders checks the cache headers.
func TestStaticCacheHeaders(t *testing.T) {
	if !webassets.Available() {
		t.Skip("interface is not built")
	}

	s := New(config.Default(), Deps{})
	h := s.Handler()

	index := do(t, h, http.MethodGet, "/", "", nil)
	if got := index.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("index.html must not be cached, got %q", got)
	}
}

// TestItoaAtoi checks the number conversions.
func TestItoaAtoi(t *testing.T) {
	cases := []int{0, 1, 42, 999, -7}
	for _, v := range cases {
		if got := itoa(v); got != intToString(v) {
			t.Fatalf("itoa(%d) = %q", v, got)
		}
	}

	for _, s := range []string{"0", "1", "42", "999"} {
		got, err := atoi(s)
		if err != nil {
			t.Fatalf("atoi(%q): %v", s, err)
		}
		if got != stringToInt(s) {
			t.Fatalf("atoi(%q) = %d", s, got)
		}
	}

	if _, err := atoi("1a"); err == nil {
		t.Fatal("expected error for non-numeric input")
	}
}

// intToString converts a number using the standard way.
func intToString(v int) string {
	return strings.TrimSpace(strings.Replace(strings.Repeat("", 0)+itoaStd(v), "\n", "", -1))
}

// itoaStd formats for comparison.
func itoaStd(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var digits []byte
	for v > 0 {
		digits = append([]byte{byte('0' + v%10)}, digits...)
		v /= 10
	}
	if neg {
		return "-" + string(digits)
	}
	return string(digits)
}

// stringToInt parses a string for comparison.
func stringToInt(s string) int {
	out := 0
	for _, r := range s {
		out = out*10 + int(r-'0')
	}
	return out
}

// TestInjectToken checks the token is injected into the page.
func TestInjectToken(t *testing.T) {
	page := []byte(`<html><body><!--cdx-token--><div id="root"></div></body></html>`)
	out := string(injectToken(page, "abc123"))

	if !strings.Contains(out, `window.__CDX_TOKEN__="abc123"`) {
		t.Fatalf("token must be injected: %s", out)
	}
	if strings.Contains(out, tokenPlaceholder) {
		t.Fatalf("placeholder must be replaced: %s", out)
	}
	if !strings.Contains(out, `<div id="root">`) {
		t.Fatalf("rest of the page must stay intact: %s", out)
	}
}

// TestInjectTokenWithoutPlaceholder checks a page without the placeholder.
func TestInjectTokenWithoutPlaceholder(t *testing.T) {
	page := []byte(`<html><body></body></html>`)
	out := injectToken(page, "abc123")
	if string(out) != string(page) {
		t.Fatal("page without a placeholder must be returned as is")
	}
}

// TestInjectTokenQuotesName checks escaping.
func TestInjectTokenQuotesName(t *testing.T) {
	page := []byte(`<!--cdx-token-->`)
	out := string(injectToken(page, `a"b`))
	if !strings.Contains(out, `\"`) {
		t.Fatalf("token must be quoted: %s", out)
	}
}

// TestServedPageCarriesWorkingToken checks the served page carries a token
// the API actually accepts.
func TestServedPageCarriesWorkingToken(t *testing.T) {
	if !webassets.Available() {
		t.Skip("interface is not built")
	}

	s := New(config.Default(), Deps{Status: func() StatusView { return StatusView{State: "running"} }})
	h := s.Handler()

	rec := do(t, h, http.MethodGet, "/", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	idx := strings.Index(body, `window.__CDX_TOKEN__="`)
	if idx < 0 {
		t.Fatalf("served page must carry the token:\n%s", body)
	}
	rest := body[idx+len(`window.__CDX_TOKEN__="`):]
	token := rest[:strings.Index(rest, `"`)]

	// The token from the page must pass authorization.
	status := do(t, h, http.MethodGet, "/api/status", token, nil)
	if status.Code != http.StatusOK {
		t.Fatalf("token from the page must authorize, got %d", status.Code)
	}
}

// TestIndexNotCached checks the page is not cached.
func TestIndexNotCached(t *testing.T) {
	if !webassets.Available() {
		t.Skip("interface is not built")
	}

	s := New(config.Default(), Deps{})
	rec := do(t, s.Handler(), http.MethodGet, "/", "", nil)
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("index must not be cached, got %q", got)
	}
}

// newAdminServer builds a server with working admin dependencies.
func newAdminServer(t *testing.T) (*Server, *adminCalls) {
	t.Helper()

	calls := &adminCalls{}
	deps := Deps{
		Providers: func() ([]ProviderView, error) {
			return []ProviderView{{
				Name: "p", HasKey: true,
				Models: []ModelView{{Provider: "p", Name: "m"}},
			}}, nil
		},
		SaveProvider: func(e ProviderEdit) error {
			calls.savedProvider = e
			if e.Name == "bad" {
				return errors.New("invalid provider")
			}
			return nil
		},
		DeleteProvider: func(name string) error {
			calls.deletedProvider = name
			return nil
		},
		SetProviderKey: func(name, key string) error {
			calls.keyProvider, calls.keyValue = name, key
			return nil
		},
		TestProvider: func(name string) (string, error) {
			if name == "broken" {
				return "", errors.New("no connection")
			}
			return "ok, 10 ms", nil
		},
		SaveModel: func(e ModelEdit) error {
			calls.savedModel = e
			if e.Name == "bad" {
				return errors.New("invalid model")
			}
			return nil
		},
		DeleteModel: func(provider, name string) error {
			calls.deletedModel = provider + "/" + name
			return nil
		},
		Plugins: func() ([]PluginView, error) {
			return []PluginView{{Name: "demo", Loaded: true, Enabled: true}}, nil
		},
		SetPlugin: func(name string, enabled bool) error {
			calls.pluginName, calls.pluginEnabled = name, enabled
			return nil
		},
		DeletePlugin: func(name string) error {
			calls.deletedPlugin = name
			return nil
		},
		MCPServers: func() ([]MCPView, error) {
			return []MCPView{{Name: "files", Transport: "stdio", Connected: true}}, nil
		},
		SaveMCP: func(e MCPEdit) error {
			calls.savedMCP = e
			return nil
		},
		DeleteMCP: func(name string) error {
			calls.deletedMCP = name
			return nil
		},
		RestartMCP: func(name string) error {
			calls.restartedMCP = name
			return nil
		},
		SavePerms: func(v PermissionsView) error {
			calls.savedPerms = v
			return nil
		},
		DangerRules: func() ([]DangerRuleView, error) {
			return []DangerRuleView{{Name: "rm", Pattern: "rm -rf", Severity: "critical"}}, nil
		},
		SaveRules: func(list []DangerRuleView) error {
			calls.savedRules = list
			return nil
		},
	}

	s := New(config.Default(), deps)
	return s, calls
}

// adminCalls records dependency calls.
type adminCalls struct {
	savedProvider   ProviderEdit
	deletedProvider string
	keyProvider     string
	keyValue        string
	savedModel      ModelEdit
	deletedModel    string
	pluginName      string
	pluginEnabled   bool
	deletedPlugin   string
	savedMCP        MCPEdit
	deletedMCP      string
	restartedMCP    string
	savedPerms      PermissionsView
	savedRules      []DangerRuleView
}

// TestSaveProvider checks the provider is saved.
func TestSaveProvider(t *testing.T) {
	s, calls := newAdminServer(t)

	rec := do(t, s.Handler(), http.MethodPost, "/api/providers", s.Token(), ProviderEdit{
		Name: "gateway", BaseURL: "http://x/v1", Protocol: "openai-chat",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if calls.savedProvider.Name != "gateway" || calls.savedProvider.BaseURL != "http://x/v1" {
		t.Fatalf("unexpected save %+v", calls.savedProvider)
	}
}

// TestSaveProviderRequiresName checks the provider name is required.
func TestSaveProviderRequiresName(t *testing.T) {
	s, _ := newAdminServer(t)
	rec := do(t, s.Handler(), http.MethodPost, "/api/providers", s.Token(), ProviderEdit{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

// TestSaveProviderRejectsInvalid checks provider validation.
func TestSaveProviderRejectsInvalid(t *testing.T) {
	s, _ := newAdminServer(t)
	rec := do(t, s.Handler(), http.MethodPost, "/api/providers", s.Token(), ProviderEdit{Name: "bad"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

// TestDeleteProvider checks the provider is removed.
func TestDeleteProvider(t *testing.T) {
	s, calls := newAdminServer(t)
	rec := do(t, s.Handler(), http.MethodDelete, "/api/providers/gone", s.Token(), nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
	if calls.deletedProvider != "gone" {
		t.Fatalf("unexpected deleted provider %q", calls.deletedProvider)
	}
}

// TestSetProviderKey checks the provider key is saved.
func TestSetProviderKey(t *testing.T) {
	s, calls := newAdminServer(t)
	rec := do(t, s.Handler(), http.MethodPut, "/api/providers/p/key", s.Token(),
		map[string]string{"key": "secret"})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
	if calls.keyProvider != "p" || calls.keyValue != "secret" {
		t.Fatalf("unexpected key call %q %q", calls.keyProvider, calls.keyValue)
	}
}

// TestTestProvider checks both a passing and a failing provider test.
func TestTestProvider(t *testing.T) {
	s, _ := newAdminServer(t)
	h := s.Handler()

	ok := do(t, h, http.MethodPost, "/api/providers/p/test", s.Token(), nil)
	if ok.Code != http.StatusOK || !strings.Contains(ok.Body.String(), `"ok":true`) {
		t.Fatalf("expected successful test, got %d %s", ok.Code, ok.Body.String())
	}

	bad := do(t, h, http.MethodPost, "/api/providers/broken/test", s.Token(), nil)
	if !strings.Contains(bad.Body.String(), `"ok":false`) {
		t.Fatalf("expected failed test, got %s", bad.Body.String())
	}
}

// TestSaveModel checks the model is saved.
func TestSaveModel(t *testing.T) {
	s, calls := newAdminServer(t)

	rec := do(t, s.Handler(), http.MethodPost, "/api/models", s.Token(), ModelEdit{
		Provider: "p", Name: "new", Remote: "remote-id", ContextWindow: 1000,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if calls.savedModel.Name != "new" || calls.savedModel.Remote != "remote-id" {
		t.Fatalf("unexpected save %+v", calls.savedModel)
	}
	if calls.savedModel.Provider != "p" {
		t.Fatalf("provider must travel with the model, got %q", calls.savedModel.Provider)
	}
}

// TestSaveModelRequiresName checks the model name is required.
func TestSaveModelRequiresName(t *testing.T) {
	s, _ := newAdminServer(t)
	rec := do(t, s.Handler(), http.MethodPost, "/api/models", s.Token(), ModelEdit{Provider: "p"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

// TestSaveModelRequiresProvider checks the provider is required.
func TestSaveModelRequiresProvider(t *testing.T) {
	s, _ := newAdminServer(t)
	rec := do(t, s.Handler(), http.MethodPost, "/api/models", s.Token(), ModelEdit{Name: "m"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

// TestSaveModelRejectsInvalid checks model validation.
func TestSaveModelRejectsInvalid(t *testing.T) {
	s, _ := newAdminServer(t)
	rec := do(t, s.Handler(), http.MethodPost, "/api/models", s.Token(),
		ModelEdit{Provider: "p", Name: "bad"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

// TestDeleteModel checks the model is removed.
func TestDeleteModel(t *testing.T) {
	s, calls := newAdminServer(t)
	rec := do(t, s.Handler(), http.MethodDelete, "/api/providers/p/models/gone", s.Token(), nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
	if calls.deletedModel != "p/gone" {
		t.Fatalf("unexpected deleted model %q", calls.deletedModel)
	}
}

// TestPluginToggle checks a plugin is enabled and disabled.
func TestPluginToggle(t *testing.T) {
	s, calls := newAdminServer(t)

	rec := do(t, s.Handler(), http.MethodPut, "/api/plugins/demo", s.Token(),
		map[string]bool{"enabled": false})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if calls.pluginName != "demo" || calls.pluginEnabled {
		t.Fatalf("unexpected plugin call %q %v", calls.pluginName, calls.pluginEnabled)
	}
}

// TestPluginListAndDelete checks listing and removal.
func TestPluginListAndDelete(t *testing.T) {
	s, calls := newAdminServer(t)
	h := s.Handler()

	list := do(t, h, http.MethodGet, "/api/plugins", s.Token(), nil)
	if !strings.Contains(list.Body.String(), "demo") {
		t.Fatalf("expected demo plugin, got %s", list.Body.String())
	}

	rec := do(t, h, http.MethodDelete, "/api/plugins/demo", s.Token(), nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
	if calls.deletedPlugin != "demo" {
		t.Fatalf("unexpected deleted plugin %q", calls.deletedPlugin)
	}
}

// TestSaveMCP checks the server is saved.
func TestSaveMCP(t *testing.T) {
	s, calls := newAdminServer(t)

	rec := do(t, s.Handler(), http.MethodPost, "/api/mcp", s.Token(), MCPEdit{
		Name: "files", Transport: "stdio", Command: "npx", Enabled: true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if calls.savedMCP.Name != "files" || calls.savedMCP.Command != "npx" {
		t.Fatalf("unexpected save %+v", calls.savedMCP)
	}
}

// TestMCPDeleteAndRestart checks removal and restart.
func TestMCPDeleteAndRestart(t *testing.T) {
	s, calls := newAdminServer(t)
	h := s.Handler()

	rec := do(t, h, http.MethodPost, "/api/mcp/files/restart", s.Token(), nil)
	if rec.Code != http.StatusOK || calls.restartedMCP != "files" {
		t.Fatalf("unexpected restart %d %q", rec.Code, calls.restartedMCP)
	}

	del := do(t, h, http.MethodDelete, "/api/mcp/files", s.Token(), nil)
	if del.Code != http.StatusNoContent || calls.deletedMCP != "files" {
		t.Fatalf("unexpected delete %d %q", del.Code, calls.deletedMCP)
	}
}

// TestSavePermissions checks permissions are saved.
func TestSavePermissions(t *testing.T) {
	s, calls := newAdminServer(t)

	rec := do(t, s.Handler(), http.MethodPut, "/api/permissions", s.Token(), PermissionsView{
		Read: "allow", Edit: "allow", Dangerous: "deny", NonInteractive: "deny",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if calls.savedPerms.Edit != "allow" || calls.savedPerms.Dangerous != "deny" {
		t.Fatalf("unexpected permissions %+v", calls.savedPerms)
	}
}

// TestDangerRules checks rules are read and saved.
func TestDangerRules(t *testing.T) {
	s, calls := newAdminServer(t)
	h := s.Handler()

	list := do(t, h, http.MethodGet, "/api/dangerous-rules", s.Token(), nil)
	if !strings.Contains(list.Body.String(), "rm -rf") {
		t.Fatalf("expected rules, got %s", list.Body.String())
	}

	save := do(t, h, http.MethodPut, "/api/dangerous-rules", s.Token(),
		map[string]any{"rules": []DangerRuleView{{Name: "n", Pattern: "p", Severity: "high"}}})
	if save.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", save.Code)
	}
	if len(calls.savedRules) != 1 || calls.savedRules[0].Name != "n" {
		t.Fatalf("unexpected rules %+v", calls.savedRules)
	}
}

// TestAdminEndpointsNeedToken checks the admin routes are protected.
func TestAdminEndpointsNeedToken(t *testing.T) {
	s, _ := newAdminServer(t)
	h := s.Handler()

	cases := []struct{ method, path string }{
		{http.MethodPost, "/api/providers"},
		{http.MethodDelete, "/api/providers/p"},
		{http.MethodPut, "/api/providers/p/key"},
		{http.MethodPost, "/api/models"},
		{http.MethodDelete, "/api/providers/p/models/m"},
		{http.MethodGet, "/api/plugins"},
		{http.MethodPut, "/api/plugins/demo"},
		{http.MethodGet, "/api/mcp"},
		{http.MethodPut, "/api/permissions"},
		{http.MethodGet, "/api/dangerous-rules"},
	}

	for _, tc := range cases {
		rec := do(t, h, tc.method, tc.path, "", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s must require a token, got %d", tc.method, tc.path, rec.Code)
		}
	}
}

// TestProviderViewFields checks the provider list carries the form fields.
//
// Without base_url the edit form showed an empty field.
func TestProviderViewFields(t *testing.T) {
	s, _ := newAdminServer(t)

	rec := do(t, s.Handler(), http.MethodGet, "/api/providers", s.Token(), nil)
	body := rec.Body.String()
	for _, field := range []string{
		`"base_url"`, `"has_key"`, `"models"`,
		`"temperature"`, `"reasoning_default"`, `"supports_reasoning_effort"`,
	} {
		if !strings.Contains(body, field) {
			t.Fatalf("providers response must contain %s: %s", field, body)
		}
	}
}

// TestInstallPlugin checks a plugin is installed by URL.
func TestInstallPlugin(t *testing.T) {
	s := New(config.Default(), Deps{
		InstallPlugin: func(url string) (PluginView, error) {
			if url == "" {
				return PluginView{}, errors.New("url is required")
			}
			return PluginView{Name: "demo", Version: "1.0.0", Loaded: true}, nil
		},
	})
	h := s.Handler()

	rec := do(t, h, http.MethodPost, "/api/plugins", s.Token(), map[string]string{
		"url": "https://example.com/demo/manifest.json",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "demo") {
		t.Fatalf("unexpected body %s", rec.Body.String())
	}

	bad := do(t, h, http.MethodPost, "/api/plugins", s.Token(), map[string]string{})
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty url, got %d", bad.Code)
	}
}

// TestDiscoverProviderModels checks the provider model list is returned.
func TestDiscoverProviderModels(t *testing.T) {
	s := New(config.Default(), Deps{
		DiscoverModels: func(name string) ([]DiscoveredModelView, error) {
			if name == "broken" {
				return nil, errors.New("endpoint is unreachable")
			}
			return []DiscoveredModelView{
				{ID: "m1", ContextWindow: 1000, MaxTokens: 100, Configured: true, LocalName: "local"},
				{ID: "m2"},
			}, nil
		},
	})
	h := s.Handler()

	ok := do(t, h, http.MethodGet, "/api/providers/p/models/discover", s.Token(), nil)
	if ok.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", ok.Code, ok.Body.String())
	}
	body := ok.Body.String()
	for _, field := range []string{`"m1"`, `"configured":true`, `"local_name":"local"`, `"m2"`} {
		if !strings.Contains(body, field) {
			t.Fatalf("discovery response must contain %s: %s", field, body)
		}
	}

	// A provider error does not break the page: it comes back as a status.
	bad := do(t, h, http.MethodGet, "/api/providers/broken/models/discover", s.Token(), nil)
	if bad.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 on provider failure, got %d", bad.Code)
	}
}

// TestDiscoverRequiresToken checks the token is required.
func TestDiscoverRequiresToken(t *testing.T) {
	s := New(config.Default(), Deps{
		DiscoverModels: func(string) ([]DiscoveredModelView, error) { return nil, nil },
	})
	rec := do(t, s.Handler(), http.MethodGet, "/api/providers/p/models/discover", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

// TestDiscoverUnsupported checks the answer when unsupported.
func TestDiscoverUnsupported(t *testing.T) {
	s := New(config.Default(), Deps{})
	rec := do(t, s.Handler(), http.MethodGet, "/api/providers/p/models/discover", s.Token(), nil)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d", rec.Code)
	}
}
