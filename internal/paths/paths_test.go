package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveWithHomeDefaults(t *testing.T) {
	env := map[string]string{}
	l := ResolveWithHome("/home/tester", func(k string) string { return env[k] })

	if l.ConfigDir != "/home/tester/.config/cdx" {
		t.Fatalf("unexpected ConfigDir: %s", l.ConfigDir)
	}
	if l.DataDir != "/home/tester/.local/share/cdx" {
		t.Fatalf("unexpected DataDir: %s", l.DataDir)
	}
	if l.DBPath != "/home/tester/.local/share/cdx/cdx.db" {
		t.Fatalf("unexpected DBPath: %s", l.DBPath)
	}
	if l.MasterKeyPath != "/home/tester/.local/share/cdx/master.key" {
		t.Fatalf("unexpected MasterKeyPath: %s", l.MasterKeyPath)
	}
	if l.SocketPath != "/home/tester/.local/state/cdx/run/cdx.sock" {
		t.Fatalf("unexpected SocketPath: %s", l.SocketPath)
	}
	if len(l.PluginDirs) != 2 {
		t.Fatalf("expected 2 plugin dirs, got %v", l.PluginDirs)
	}
	if l.PluginDirs[0] != "/home/tester/.local/share/cdx/plugins" {
		t.Fatalf("unexpected user plugin dir: %s", l.PluginDirs[0])
	}
	if l.PluginDirs[1] != "/usr/share/cdx/plugins" {
		t.Fatalf("unexpected system plugin dir: %s", l.PluginDirs[1])
	}
}

func TestResolveWithHomeOverrides(t *testing.T) {
	env := map[string]string{
		"XDG_RUNTIME_DIR":       "/run/user/1000",
		"CDX_DATA_DIR":          "/custom/data",
		"CDX_SYSTEM_PLUGIN_DIR": "/custom/sys-plugins",
	}
	l := ResolveWithHome("/home/tester", func(k string) string { return env[k] })

	if l.DataDir != "/custom/data" {
		t.Fatalf("unexpected DataDir: %s", l.DataDir)
	}
	if l.SocketPath != "/run/user/1000/cdx/cdx.sock" {
		t.Fatalf("unexpected SocketPath: %s", l.SocketPath)
	}
	if l.PluginDirs[1] != "/custom/sys-plugins" {
		t.Fatalf("unexpected sys plugin dir: %s", l.PluginDirs[1])
	}
}

func TestEnsureDirs(t *testing.T) {
	tmp := t.TempDir()
	l := ResolveWithHome(tmp, func(string) string { return "" })
	if err := l.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs failed: %v", err)
	}

	for _, dir := range []string{l.ConfigDir, l.DataDir, l.StateDir, l.RuntimeDir, l.LogDir, filepath.Join(l.DataDir, "plugins")} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("expected directory %s to exist: %v", dir, err)
		}
		if !info.IsDir() {
			t.Fatalf("expected %s to be a directory", dir)
		}
	}
}
