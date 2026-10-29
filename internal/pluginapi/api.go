// Package pluginapi gives plugins reach to files, shell and network.
//
// These core capabilities are never exposed to the model as tools: the model
// only sees tools from plugin manifests and MCP servers. This package is an
// internal API for plugins, the way a standard library serves a program: a
// plugin builds its own tools on top of it.
package pluginapi

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/yourok/cdx/internal/perm"
)

// Limits bounds a single operation.
type Limits struct {
	// MaxReadBytes caps the size of a file being read.
	MaxReadBytes int
	// MaxOutputBytes caps command output and network responses.
	MaxOutputBytes int
	// TimeoutMS caps the time of a command or request.
	TimeoutMS int
	// MaxResults caps search and walk hits.
	MaxResults int
}

// DefaultLimits returns the built-in bounds.
//
// Without them a single command such as a whole-disk search would stall the
// turn, and a hundred-megabyte reply would eat memory.
func DefaultLimits() Limits {
	return Limits{
		MaxReadBytes:   1 << 20,
		MaxOutputBytes: 32 << 10,
		TimeoutMS:      60000,
		MaxResults:     200,
	}
}

// API performs plugin operations.
//
// Permissions are checked on every call: a plugin never reaches files or the
// network without the user deciding, whenever the level requires it.
type API struct {
	limits Limits
	perms  *perm.Manager
	root   string
	// rootFn resolves the root at call time, which wins over root.
	//
	// The root depends on the turn: a plugin loads when the daemon starts but
	// works in the user directory, known only while a turn runs.
	rootFn func() string

	mu  sync.Mutex
	ask Asker
}

// Options configures an API.
type Options struct {
	Limits Limits
	Perms  *perm.Manager
	// Root is the plugin working directory; empty means the process one.
	Root string
	// RootFn resolves the root at call time and wins over Root.
	RootFn func() string
	// Ask asks the user; nil denies wherever confirmation is mandatory.
	Ask Asker
}

// New creates the API of one plugin.
func New(opts Options) *API {
	limits := opts.Limits
	if limits.MaxReadBytes <= 0 {
		limits.MaxReadBytes = DefaultLimits().MaxReadBytes
	}
	if limits.MaxOutputBytes <= 0 {
		limits.MaxOutputBytes = DefaultLimits().MaxOutputBytes
	}
	if limits.TimeoutMS <= 0 {
		limits.TimeoutMS = DefaultLimits().TimeoutMS
	}
	if limits.MaxResults <= 0 {
		limits.MaxResults = DefaultLimits().MaxResults
	}

	root := opts.Root
	if root == "" {
		if cwd, err := os.Getwd(); err == nil {
			root = cwd
		}
	}
	return &API{limits: limits, perms: opts.Perms, root: root, rootFn: opts.RootFn, ask: opts.Ask}
}

// DeniedError is a permission refusal carrying the action it refused.
//
// The description is for the interface: it tells the user what would happen
// without exposing internal tool names.
type DeniedError struct {
	// Action describes the attempt, for example "run: git log".
	Action string
	// Level is the permission level the action was matched to.
	Level string
}

// Error returns the refusal text.
func (e *DeniedError) Error() string {
	return "permission denied: " + e.Action
}

// allow checks the permission of an operation and asks for confirmation.
//
// It returns an error when the action is forbidden or refused.
func (a *API) allow(tool, level, summary string) error {
	if a.perms == nil {
		// Without a permission manager the safe default is refusal.
		return fmt.Errorf("permission denied: %s requires a permission manager", tool)
	}

	decision := a.perms.Check(tool, level, summary)
	if decision.Allowed {
		return nil
	}
	if !decision.NeedAsk {
		return fmt.Errorf("permission denied: %s", summary)
	}
	return a.askDecision(level, tool, summary)
}

// resolve turns a path into an absolute one inside the working directory.
//
// Escaping the root is refused: a plugin must not touch foreign files through
// a typo in a path.
func (a *API) resolve(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("path is required")
	}

	root := a.rootAt()
	full := path
	if !filepath.IsAbs(full) {
		full = filepath.Join(root, full)
	}
	full = filepath.Clean(full)

	root = filepath.Clean(root)
	if root == "" {
		return full, nil
	}
	// The separator matters: otherwise "/work2" would count as inside "/work".
	if full != root && !strings.HasPrefix(full, root+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q is outside the working directory", path)
	}
	return full, nil
}

// rootAt returns the root in force right now.
func (a *API) rootAt() string {
	if a.rootFn != nil {
		if root := a.rootFn(); root != "" {
			return root
		}
	}
	return a.root
}
