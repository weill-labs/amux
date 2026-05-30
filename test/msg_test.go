package test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type msgCLISendJSON struct {
	ID string `json:"id"`
}

func runHarnessCLIWithInput(t *testing.T, h *ServerHarness, input string, args ...string) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), runCmdTimeout)
	defer cancel()

	cmd := h.commandWithContext(ctx, args...)
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		t.Fatalf("amux %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	if exitErr != nil && exitErr.ExitCode() != 0 {
		t.Fatalf("amux %s exit = %d, want 0\n%s", strings.Join(args, " "), exitErr.ExitCode(), out)
	}
	return string(out)
}

func parseMsgCLISendID(t *testing.T, raw string) string {
	t.Helper()

	var out msgCLISendJSON
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("json.Unmarshal(msg send output): %v\nraw:\n%s", err, raw)
	}
	if out.ID == "" {
		t.Fatalf("msg send output missing id:\n%s", raw)
	}
	return out.ID
}

func TestMsgCLISendReadsStdinAndBodyFile(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)
	h.runCmd("spawn", "--at", "pane-1")

	stdinOut := runHarnessCLIWithInput(t, h, "stdin body\nsecond line\n", "msg", "send", "--from", "pane-1", "--to", "pane-2", "--subject", "stdin", "--format", "json")
	stdinID := parseMsgCLISendID(t, stdinOut)
	stdinRead := h.runCmd("msg", "read", stdinID, "--for", "pane-2")
	if !strings.Contains(stdinRead, "stdin body\nsecond line") {
		t.Fatalf("stdin message read output = %q, want stdin body", stdinRead)
	}

	bodyPath := filepath.Join(t.TempDir(), "message.txt")
	if err := os.WriteFile(bodyPath, []byte("file body\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(body): %v", err)
	}
	fileOut := h.runCmd("msg", "send", "--from", "pane-1", "--to", "pane-2", "--subject", "file", "--body-file", bodyPath, "--format", "json")
	fileID := parseMsgCLISendID(t, fileOut)
	fileRead := h.runCmd("msg", "read", fileID, "--for", "pane-2")
	if !strings.Contains(fileRead, "file body") {
		t.Fatalf("body-file message read output = %q, want file body", fileRead)
	}
}
