package mux

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGitBranch(t *testing.T) {
	t.Parallel()

	t.Run("empty dir", func(t *testing.T) {
		t.Parallel()
		if got := GitBranch(""); got != "" {
			t.Errorf("GitBranch(\"\") = %q, want \"\"", got)
		}
	})

	t.Run("nonexistent dir", func(t *testing.T) {
		t.Parallel()
		if got := GitBranch("/nonexistent-path-xyz"); got != "" {
			t.Errorf("GitBranch(nonexistent) = %q, want \"\"", got)
		}
	})

	t.Run("not a git repo", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if got := GitBranch(dir); got != "" {
			t.Errorf("GitBranch(non-repo) = %q, want \"\"", got)
		}
	})

	t.Run("git repo returns branch", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()

		// Initialize a git repo with a commit
		run := func(args ...string) {
			t.Helper()
			cmd := exec.Command(args[0], args[1:]...)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(),
				"GIT_AUTHOR_NAME=test",
				"GIT_AUTHOR_EMAIL=test@test.com",
				"GIT_COMMITTER_NAME=test",
				"GIT_COMMITTER_EMAIL=test@test.com",
			)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%v failed: %v\n%s", args, err, out)
			}
		}

		run("git", "init", "-b", "test-branch")
		os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hi"), 0644)
		run("git", "add", ".")
		run("git", "commit", "-m", "init")

		got := GitBranch(dir)
		if got != "test-branch" {
			t.Errorf("GitBranch() = %q, want %q", got, "test-branch")
		}
	})
}
