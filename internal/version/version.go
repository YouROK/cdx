// Package version holds build information and protocol constants.
package version

// Build values injected by the linker.
var (
	Version = "0.2.0-dev"
	Commit  = "unknown"
	Date    = "unknown"
)

// Protocol is the IPC wire protocol version.
const Protocol = 1
