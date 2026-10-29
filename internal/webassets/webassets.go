// Package webassets embeds the built user interface.
package webassets

import (
	"embed"
	"io/fs"
	"net/http"
)

// distDir holds the built interface assets.
//
//go:embed all:dist
var distDir embed.FS

// FS returns the file system with the built assets.
func FS() http.FileSystem {
	sub, err := fs.Sub(distDir, "dist")
	if err != nil {
		return nil
	}
	return http.FS(sub)
}

// Available reports whether the assets are built and not empty.
func Available() bool {
	entries, err := distDir.ReadDir("dist")
	if err != nil || len(entries) == 0 {
		return false
	}
	// A lone .gitkeep placeholder does not count as a build.
	for _, e := range entries {
		if e.Name() != ".gitkeep" {
			return true
		}
	}
	return false
}
