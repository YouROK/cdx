// cdx is the agent client and daemon in one binary.
package main

import (
	"os"

	"github.com/yourok/cdx/internal/cli"
	"github.com/yourok/cdx/internal/logx"
)

func main() {
	code := cli.Main(os.Args[1:])
	logx.Close()
	os.Exit(code)
}
