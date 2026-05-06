package testenv

import (
	"context"
	"os"
	"os/exec"
	"strings"
)

var blockedEnvKeys = []string{
	"AMUX_CLIENT_CAPABILITIES",
	"AMUX_COLOR_PROFILE",
	"AMUX_LOG_DIR",
	"AMUX_MAIN_HELPER",
	"AMUX_PANE",
	"AMUX_SESSION",
	"CLICOLOR",
	"CLICOLOR_FORCE",
	"COLORTERM",
	"NO_COLOR",
	"SSH_CLIENT",
	"SSH_CONNECTION",
	"SSH_TTY",
	"TERM",
	"TERM_PROGRAM",
	"TMUX",
}

func HermeticAmuxEnv() []string {
	env := append([]string{}, os.Environ()...)
	for _, key := range blockedEnvKeys {
		env = RemoveEnvKey(env, key)
	}
	return append(env, "TERM=xterm-256color")
}

func HermeticMainEnv() []string {
	return append(HermeticAmuxEnv(), "AMUX_MAIN_HELPER=1")
}

func NewCommand(name string, args ...string) *exec.Cmd {
	return NewCommandContext(context.Background(), name, args...)
}

func NewCommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = HermeticAmuxEnv()
	return cmd
}

func RemoveEnvKey(env []string, key string) []string {
	prefix := key + "="
	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}
