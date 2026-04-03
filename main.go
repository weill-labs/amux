package main

import (
	"os"

	clicmd "github.com/weill-labs/amux/internal/cli"
)

// BuildCommit can be set via -ldflags "-X main.BuildCommit=abc1234".
// Falls back to VCS info from runtime/debug at startup.
var BuildCommit string

func run() int {
	return clicmd.Run(BuildCommit, os.Args[1:])
}

func main() {
	os.Exit(run())
}
