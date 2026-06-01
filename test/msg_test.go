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

type msgCLIDrainStatusJSON struct {
	Unread             int      `json:"unread"`
	Unacked            int      `json:"unacked"`
	Pending            int      `json:"pending"`
	PendingFingerprint string   `json:"pending_fingerprint"`
	PendingIDs         []string `json:"pending_ids"`
}

type msgCLIThreadJSON []struct {
	ID        string `json:"id"`
	Body      string `json:"body"`
	ThreadID  string `json:"thread_id"`
	InReplyTo string `json:"in_reply_to"`
	Sender    struct {
		Name string `json:"name"`
	} `json:"sender"`
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

func parseMsgCLIDrainStatus(t *testing.T, raw string) msgCLIDrainStatusJSON {
	t.Helper()

	var out msgCLIDrainStatusJSON
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("json.Unmarshal(msg drain-status output): %v\nraw:\n%s", err, raw)
	}
	return out
}

func parseMsgCLIThread(t *testing.T, raw string) msgCLIThreadJSON {
	t.Helper()

	var out msgCLIThreadJSON
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("json.Unmarshal(msg thread output): %v\nraw:\n%s", err, raw)
	}
	return out
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

func TestMsgCLIThreadDumpsConversation(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)
	h.runCmd("spawn", "--at", "pane-1")

	rootID := parseMsgCLISendID(t, h.runCmd("msg", "send", "--from", "pane-1", "--to", "pane-2", "--topic", "handoff", "--body", "root body", "--format", "json"))
	replyID := parseMsgCLISendID(t, h.runCmd("msg", "reply", rootID, "--from", "pane-2", "--body", "reply body", "--format", "json"))
	h.runCmd("msg", "send", "--from", "pane-1", "--to", "pane-2", "--topic", "other", "--body", "other body")

	byTopic := parseMsgCLIThread(t, h.runCmd("msg", "thread", "handoff", "--format", "json"))
	if len(byTopic) != 2 {
		t.Fatalf("thread by topic length = %d, want 2", len(byTopic))
	}
	if byTopic[0].ID != rootID || byTopic[0].Sender.Name != "pane-1" || byTopic[0].Body != "root body" {
		t.Fatalf("thread by topic root = %#v, want pane-1 root body", byTopic[0])
	}
	if byTopic[1].ID != replyID || byTopic[1].Sender.Name != "pane-2" || byTopic[1].Body != "reply body" {
		t.Fatalf("thread by topic reply = %#v, want pane-2 reply body", byTopic[1])
	}

	byMessage := parseMsgCLIThread(t, h.runCmd("msg", "thread", replyID, "--format", "json"))
	if len(byMessage) != 2 || byMessage[0].ID != rootID || byMessage[1].ID != replyID || byMessage[1].InReplyTo != rootID {
		t.Fatalf("thread by message = %#v, want linked root/reply", byMessage)
	}

	text := h.runCmd("msg", "thread", rootID)
	if !strings.Contains(text, "msg-000001 from pane-1") || !strings.Contains(text, "root body") || !strings.Contains(text, "reply body") {
		t.Fatalf("thread text output = %q, want sender and bodies", text)
	}
}

func TestMsgCLIDrainStatusReportsReadAckPendingState(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)
	h.runCmd("spawn", "--at", "pane-1")

	firstID := parseMsgCLISendID(t, h.runCmd("msg", "send", "--from", "pane-1", "--to", "pane-2", "--body", "first", "--format", "json"))
	secondID := parseMsgCLISendID(t, h.runCmd("msg", "send", "--from", "pane-1", "--to", "pane-2", "--body", "second", "--format", "json"))

	if got := h.runCmd("msg", "drain-status", "pane-2"); got != "2\n" {
		t.Fatalf("drain-status text = %q, want 2", got)
	}
	initial := parseMsgCLIDrainStatus(t, h.runCmd("msg", "drain-status", "pane-2", "--format", "json"))
	if initial.Unread != 2 || initial.Unacked != 2 || initial.Pending != 2 || initial.PendingFingerprint == "" {
		t.Fatalf("initial drain-status = %#v, want two unread/unacked pending messages", initial)
	}

	h.runCmd("msg", "read", firstID, "--for", "pane-2")
	readOnly := parseMsgCLIDrainStatus(t, h.runCmd("msg", "drain-status", "pane-2", "--format", "json"))
	if readOnly.Unread != 1 || readOnly.Unacked != 2 || readOnly.Pending != 2 {
		t.Fatalf("read-only drain-status = %#v, want first message still pending for ack", readOnly)
	}
	if readOnly.PendingFingerprint == initial.PendingFingerprint {
		t.Fatalf("pending fingerprint did not change after read: %q", readOnly.PendingFingerprint)
	}

	h.runCmd("msg", "ack", firstID, "--for", "pane-2", "--status", "ok")
	h.runCmd("msg", "ack", secondID, "--for", "pane-2", "--status", "seen")
	ackOnly := parseMsgCLIDrainStatus(t, h.runCmd("msg", "drain-status", "pane-2", "--format", "json"))
	if ackOnly.Unread != 1 || ackOnly.Unacked != 0 || ackOnly.Pending != 1 {
		t.Fatalf("ack-only drain-status = %#v, want second message pending for read", ackOnly)
	}
	if len(ackOnly.PendingIDs) != 1 || ackOnly.PendingIDs[0] != secondID {
		t.Fatalf("ack-only pending ids = %v, want [%s]", ackOnly.PendingIDs, secondID)
	}
}
