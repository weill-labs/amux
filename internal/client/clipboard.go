package client

import (
	"encoding/base64"
	"io"
	"os"
	"os/exec"
	"strings"
)

var clipboardStdout io.Writer = os.Stdout

var runClipboardCommand = func(cmd []string, text string) error {
	c := exec.Command(cmd[0], cmd[1:]...)
	c.Stdin = strings.NewReader(text)
	return c.Run()
}

// CopyToClipboard copies text to the local clipboard when possible. When the
// interactive client is running under SSH, prefer OSC 52 so the clipboard write
// lands back on the source terminal instead of the remote machine.
func CopyToClipboard(text string) {
	if text == "" {
		return
	}
	if preferOSC52Clipboard(os.LookupEnv) {
		writeOSC52Clipboard(clipboardStdout, text)
		return
	}
	if trySystemClipboard(text) {
		return
	}
	writeOSC52Clipboard(clipboardStdout, text)
}

func preferOSC52Clipboard(lookup envLookup) bool {
	return envSet(lookup, "SSH_CONNECTION") || envSet(lookup, "SSH_CLIENT") || envSet(lookup, "SSH_TTY")
}

func trySystemClipboard(text string) bool {
	for _, cmd := range [][]string{
		{"pbcopy"},
		{"xclip", "-selection", "clipboard"},
		{"xsel", "--clipboard", "--input"},
	} {
		if runClipboardCommand(cmd, text) == nil {
			return true
		}
	}
	return false
}

func writeOSC52Clipboard(w io.Writer, text string) {
	if w == nil {
		return
	}
	_, _ = io.WriteString(w, osc52ClipboardSequence(text))
}

func osc52ClipboardSequence(text string) string {
	return "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\a"
}
