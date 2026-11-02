// Command cdx is the entry point for the cdx CLI and background daemon.
package main

import (
	"fmt"
	"os"

	"github.com/yourok/cdx/internal/cli"
)

func main() {
	app := cli.NewApp(nil)
	if err := app.Run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
