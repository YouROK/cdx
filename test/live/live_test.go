//go:build live

package live

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/yourok/cdx/internal/cli"
	"github.com/yourok/cdx/internal/daemon"
	"github.com/yourok/cdx/internal/paths"
)

func repoPluginsDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve caller path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "plugins"))
}

func TestLiveBeamFreeModelAndTools(t *testing.T) {
	apiKey := os.Getenv("CDX_LIVE_API_KEY")
	if apiKey == "" {
		t.Skip("CDX_LIVE_API_KEY is not set")
	}

	tmp := t.TempDir()
	layout := paths.ResolveWithHome(tmp, func(string) string { return "" })
	layout.PluginDirs = []string{repoPluginsDir(t)}

	d, err := daemon.New(context.Background(), daemon.Options{
		Layout:   layout,
		HTTPAddr: "127.0.0.1:0",
	})
	if err != nil {
		t.Fatalf("daemon.New failed: %v", err)
	}
	if err := d.Start(); err != nil {
		t.Fatalf("daemon.Start failed: %v", err)
	}
	defer d.Stop()

	baseURL := "http://" + d.HTTPAddr()

	// 1. Configure opencode-go provider with the encrypted API key via the providers plugin HTTP API.
	provBody := `{"name":"opencode-go","protocol":"openai-chat","base_url":"https://opencode.ai/zen/go/v1","api_key":"` + apiKey + `"}`
	resp1, err := http.Post(baseURL+"/api/plugins/providers/providers", "application/json", bytes.NewBufferString(provBody))
	if err != nil || resp1.StatusCode != http.StatusOK {
		t.Fatalf("failed to configure provider: status=%v err=%v", resp1.StatusCode, err)
	}
	resp1.Body.Close()

	// 2. Configure beam-free model via the providers plugin HTTP API.
	modelBody := `{"provider":"opencode-go","name":"beam-free","remote":"beam-free","context_window":128000,"max_tokens":1024,"temperature":0.2,"default":true}`
	resp2, err := http.Post(baseURL+"/api/plugins/providers/models", "application/json", bytes.NewBufferString(modelBody))
	if err != nil || resp2.StatusCode != http.StatusOK {
		t.Fatalf("failed to configure model: status=%v err=%v", resp2.StatusCode, err)
	}
	resp2.Body.Close()

	// 3. Create a test file on disk for the read_file tool to read.
	secretFile := filepath.Join(tmp, "magic.txt")
	if err := os.WriteFile(secretFile, []byte("MAGIC_CODE_7749"), 0o644); err != nil {
		t.Fatalf("write magic.txt: %v", err)
	}

	var stdout, stderr bytes.Buffer
	app := &cli.App{
		Layout: layout,
		Stdin:  strings.NewReader("y\n"),
		Stdout: &stdout,
		Stderr: &stderr,
	}

	// 4. Ask the model to use read_file on magic.txt and report the code.
	prompt := "Use the read_file tool to read the file at " + secretFile + " and tell me the exact magic code inside."
	if err := app.Run([]string{prompt}); err != nil {
		t.Fatalf("live CLI prompt failed: %v (stderr: %s)", err, stderr.String())
	}

	out := stdout.String()
	t.Logf("Live model output: %s", strings.TrimSpace(out))
	if !strings.Contains(out, "MAGIC_CODE_7749") {
		t.Fatalf("expected output to contain MAGIC_CODE_7749, got: %q", out)
	}
}
