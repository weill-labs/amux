package main

import (
	"os"
)

// BuildCommit can be set via -ldflags "-X main.BuildCommit=abc1234".
// Falls back to VCS info from runtime/debug at startup.
var BuildCommit string

func main() {
	os.Exit(runMain(os.Args[1:], defaultCLIRuntime()))
}
