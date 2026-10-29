// Package version holds build information.
package version

// The values are set by the linker at build time.
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

// Protocol is the IPC protocol version.
const Protocol = 1
