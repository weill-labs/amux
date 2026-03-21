package mux

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

const gitTimeout = 200 * time.Millisecond

// GitBranch returns the current git branch for a directory.
// Returns "" if not in a git repo, on a detached HEAD, or on error.
func GitBranch(dir string) string {
	if dir == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return ""
	}
	branch := strings.TrimSpace(string(out))
	if branch == "HEAD" {
		return "" // detached HEAD
	}
	return branch
}
