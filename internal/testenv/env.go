package testenv

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/weill-labs/amux/internal/proto"
)

var blockedEnvKeys = []string{
	"AMUX_CLIENT_CAPABILITIES",
	"AMUX_COLOR_PROFILE",
	"AMUX_LOG_DIR",
	"AMUX_MAIN_HELPER",
	"AMUX_PANE",
	"AMUX_SESSION",
	"AMUX_SOCKET_DIR",
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

func IsolateSocketDirForTestProcess(prefix string) (func(), error) {
	socketDir, err := os.MkdirTemp(shortSocketTempRoot(), sanitizeSocketDirPrefix(prefix)+"-")
	if err != nil {
		return nil, fmt.Errorf("creating isolated socket dir: %w", err)
	}
	if err := os.Setenv(proto.SocketDirEnv, socketDir); err != nil {
		_ = os.RemoveAll(socketDir)
		return nil, fmt.Errorf("setting %s: %w", proto.SocketDirEnv, err)
	}

	return func() { _ = os.RemoveAll(socketDir) }, nil
}

func shortSocketTempRoot() string {
	if info, err := os.Stat("/tmp"); err == nil && info.IsDir() {
		return "/tmp"
	}
	return os.TempDir()
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

func sanitizeSocketDirPrefix(prefix string) string {
	prefix = strings.NewReplacer("/", "-", "\\", "-").Replace(prefix)
	prefix = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		default:
			return '-'
		}
	}, prefix)
	prefix = strings.Trim(prefix, "-")
	if prefix == "" || prefix == "." {
		return "amux-test"
	}
	if len(prefix) > 6 {
		return prefix[:6]
	}
	return prefix
}
