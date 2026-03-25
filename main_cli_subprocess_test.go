package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestMainCLISubprocessHelper(t *testing.T) {
	if os.Getenv("AMUX_MAIN_HELPER") != "1" {
		return
	}

	args := os.Args[1:]
	for i, arg := range args {
		if arg == "--" {
			os.Args = append([]string{"amux"}, args[i+1:]...)
			main()
			return
		}
	}
	t.Fatal("missing -- separator")
}

func runHermeticMain(t *testing.T, args ...string) (output string, exitCode int) {
	t.Helper()

	cmd := newHermeticMainCmd(t, args...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}

	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("helper error = %v\n%s", err, out)
	}
	return string(out), exitErr.ExitCode()
}

func newHermeticMainCmd(t *testing.T, args ...string) *exec.Cmd {
	t.Helper()

	session := hermeticMainSession(t.Name())
	cmdArgs := append([]string{"-test.run=TestMainCLISubprocessHelper", "--", "-s", session}, args...)
	cmd := exec.Command(os.Args[0], cmdArgs...)
	cmd.Env = hermeticMainEnv()
	return cmd
}

func hermeticMainSession(testName string) string {
	var b strings.Builder
	for _, r := range testName {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
		if b.Len() >= 32 {
			break
		}
	}
	suffix := strings.Trim(b.String(), "-")
	if suffix == "" {
		suffix = "main-usage"
	}
	return fmt.Sprintf("usage-%d-%s", os.Getpid(), suffix)
}

func hermeticMainEnv() []string {
	env := append([]string{}, os.Environ()...)
	for _, key := range []string{
		"AMUX_MAIN_HELPER",
		"AMUX_PANE",
		"AMUX_SESSION",
		"TMUX",
		"SSH_CONNECTION",
		"SSH_CLIENT",
		"SSH_TTY",
		"TERM",
	} {
		env = removeEnvKey(env, key)
	}
	env = append(env,
		"AMUX_MAIN_HELPER=1",
		"TERM=xterm-256color",
	)
	return env
}

func removeEnvKey(env []string, key string) []string {
	prefix := key + "="
	filtered := env[:0]
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}
