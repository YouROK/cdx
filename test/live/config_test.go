//go:build live

package live

import (
	"github.com/yourok/cdx/internal/config"
)

// configDefaults returns a config with permissive permissions.
//
// Live checks never ask for confirmation: there may be no terminal.
func configDefaults() *config.Config {
	cfg := config.Default()
	cfg.Perms.Read = "allow"
	cfg.Perms.Edit = "allow"
	cfg.Perms.Dangerous = "deny"
	cfg.Agent.MaxIterations = 8
	return cfg
}
