package client

import (
	"encoding/base64"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// clipboardDeps holds injectable dependencies for clipboard operations.
type clipboardDeps struct {
	stdout io.Writer
	runCmd func(cmd []string, text string) error
}

// defaultClipboardDeps returns the production clipboard dependencies.
func defaultClipboardDeps() clipboardDeps {
	return clipboardDeps{
		stdout: os.Stdout,
		runCmd: func(cmd []string, text string) error {
			c := exec.Command(cmd[0], cmd[1:]...)
			c.Stdin = strings.NewReader(text)
			return c.Run()
		},
	}
}

// copyToClipboardLocal copies text to the local clipboard when possible. When the
// interactive client is running under SSH, prefer OSC 52 so the clipboard write
// lands back on the source terminal instead of the remote machine.
func copyToClipboardLocal(deps clipboardDeps, text string) {
	if text == "" {
		return
	}
	if preferOSC52Clipboard(os.LookupEnv) {
		writeOSC52Clipboard(deps.stdout, text)
		return
	}
	if trySystemClipboard(deps.runCmd, text) {
		return
	}
	writeOSC52Clipboard(deps.stdout, text)
}

func preferOSC52Clipboard(lookup envLookup) bool {
	return envSet(lookup, "SSH_CONNECTION") || envSet(lookup, "SSH_CLIENT") || envSet(lookup, "SSH_TTY")
}

func trySystemClipboard(runCmd func([]string, string) error, text string) bool {
	for _, cmd := range [][]string{
		{"pbcopy"},
		{"xclip", "-selection", "clipboard"},
		{"xsel", "--clipboard", "--input"},
	} {
		if runCmd(cmd, text) == nil {
			return true
		}
	}
	return false
}

func writeOSC52Clipboard(w io.Writer, text string) {
	if w == nil {
		return
	}
	_, _ = io.WriteString(w, osc52ClipboardOutput(os.LookupEnv, text))
}

func osc52ClipboardSequence(text string) string {
	return "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\a"
}

func osc52ClipboardOutput(lookup envLookup, text string) string {
	seq := osc52ClipboardSequence(text)
	if envSet(lookup, "TMUX") {
		return ansi.TmuxPassthrough(seq)
	}
	return seq
}
