package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yourok/cdx/internal/config"
	"github.com/yourok/cdx/internal/i18n"
)

// setupConfig prepares a config on temporary directories.
func setupConfig(t *testing.T) *config.Config {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("CDX_DATA_DIR", filepath.Join(dir, "data"))
	t.Setenv("CDX_CONFIG", filepath.Join(dir, "config.yaml"))

	cfg := config.Default()
	return cfg
}

// TestDoctorReportsChecks checks that doctor runs its checks.
func TestDoctorReportsChecks(t *testing.T) {
	cfg := setupConfig(t)
	if err := i18n.Init("en"); err != nil {
		t.Fatalf("i18n: %v", err)
	}

	code := runDoctor(&Options{}, cfg)
	// The daemon is not running: a warning, not an error.
	if code != ExitOK {
		t.Fatalf("expected exit 0 with warnings only, got %d", code)
	}
}

// TestDoctorLocalized checks translation of the check names.
func TestDoctorLocalized(t *testing.T) {
	cfg := setupConfig(t)

	if err := i18n.Init("ru"); err != nil {
		t.Fatalf("i18n: %v", err)
	}
	t.Cleanup(func() { _ = i18n.Init("en") })

	// The translation comes from the catalog, not from the code.
	if got := i18n.Tr("cli.doctor.config"); got != "конфиг" {
		t.Fatalf("unexpected translation %q", got)
	}
	if got := i18n.Tr("cli.doctor.ok"); got != "ОК" {
		t.Fatalf("unexpected status translation %q", got)
	}

	if code := runDoctor(&Options{}, cfg); code != ExitOK {
		t.Fatalf("expected exit 0, got %d", code)
	}
}

// TestDoctorFailsOnBadPath checks the error code for unreachable paths.
func TestDoctorFailsOnBadPath(t *testing.T) {
	dir := t.TempDir()
	// The data directory points at a file, so nothing can be created in it.
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("CDX_DATA_DIR", filepath.Join(blocker, "data"))

	if err := i18n.Init("en"); err != nil {
		t.Fatalf("i18n: %v", err)
	}

	code := runDoctor(&Options{}, config.Default())
	if code != ExitConfig {
		t.Fatalf("expected exit %d for broken data dir, got %d", ExitConfig, code)
	}
}
