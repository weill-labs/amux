package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/weill-labs/amux/internal/mux"
)

type msgCommandSendJSON struct {
	ID         string `json:"id"`
	Subject    string `json:"subject"`
	Recipients []struct {
		ID   uint32 `json:"id"`
		Name string `json:"name"`
	} `json:"recipients"`
}

type msgCommandInboxJSON []struct {
	ID        string `json:"id"`
	Subject   string `json:"subject"`
	BodySize  int    `json:"body_size"`
	PartCount int    `json:"part_count"`
}

func setupMsgCommandSession(t *testing.T) (*Server, *Session, func()) {
	t.Helper()

	srv, sess, cleanup := newCommandTestSession(t)
	p1 := newTestPane(sess, 1, "pane-1")
	p2 := newTestPane(sess, 2, "pane-2")
	p3 := newTestPane(sess, 3, "shared")
	p4 := newTestPane(sess, 4, "shared")
	w := newTestWindowWithPanes(t, sess, 1, "main", p1, p2, p3, p4)
	setSessionLayoutForTest(t, sess, w.ID, []*mux.Window{w}, p1, p2, p3, p4)
	return srv, sess, cleanup
}

func parseMsgCommandSendJSON(t *testing.T, raw string) msgCommandSendJSON {
	t.Helper()

	var out msgCommandSendJSON
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("json.Unmarshal(send output): %v\nraw:\n%s", err, raw)
	}
	if out.ID == "" {
		t.Fatalf("send JSON id is empty:\n%s", raw)
	}
	return out
}

func parseMsgCommandInboxJSON(t *testing.T, raw string) msgCommandInboxJSON {
	t.Helper()

	var out msgCommandInboxJSON
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("json.Unmarshal(inbox output): %v\nraw:\n%s", err, raw)
	}
	return out
}

func TestMsgCommandSendInboxReadAck(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := setupMsgCommandSession(t)
	defer cleanup()

	first := runTestCommand(t, srv, sess, "msg", "send", "--from", "pane-1", "--to", "pane-2", "--subject", "Review", "--body", "please review", "--format", "json")
	if first.cmdErr != "" {
		t.Fatalf("msg send by name error: %s", first.cmdErr)
	}
	firstMsg := parseMsgCommandSendJSON(t, first.output)
	if firstMsg.Subject != "Review" || len(firstMsg.Recipients) != 1 || firstMsg.Recipients[0].Name != "pane-2" {
		t.Fatalf("send by name JSON = %#v, want frozen pane-2 recipient", firstMsg)
	}

	second := runTestCommand(t, srv, sess, "msg", "send", "--from", "pane-1", "--to", "2", "--subject", "By ID", "--body", "body from id", "--format", "json")
	if second.cmdErr != "" {
		t.Fatalf("msg send by ID error: %s", second.cmdErr)
	}
	secondMsg := parseMsgCommandSendJSON(t, second.output)
	if len(secondMsg.Recipients) != 1 || secondMsg.Recipients[0].ID != 2 {
		t.Fatalf("send by ID JSON = %#v, want recipient ID 2", secondMsg)
	}

	inbox := runTestCommand(t, srv, sess, "msg", "inbox", "pane-2", "--unread", "--format", "json")
	if inbox.cmdErr != "" {
		t.Fatalf("msg inbox error: %s", inbox.cmdErr)
	}
	unread := parseMsgCommandInboxJSON(t, inbox.output)
	if len(unread) != 2 {
		t.Fatalf("unread inbox length = %d, want 2\n%s", len(unread), inbox.output)
	}
	if unread[0].ID != firstMsg.ID || unread[0].BodySize != len("please review") || unread[0].PartCount != 1 {
		t.Fatalf("first unread summary = %#v, want first message summary", unread[0])
	}

	read := runTestCommand(t, srv, sess, "msg", "read", firstMsg.ID, "--for", "pane-2")
	if read.cmdErr != "" {
		t.Fatalf("msg read error: %s", read.cmdErr)
	}
	if !strings.Contains(read.output, "please review") {
		t.Fatalf("msg read output = %q, want full body", read.output)
	}

	ack := runTestCommand(t, srv, sess, "msg", "ack", secondMsg.ID, "--for", "pane-2", "--status", "ok")
	if ack.cmdErr != "" {
		t.Fatalf("msg ack error: %s", ack.cmdErr)
	}
	afterAck := runTestCommand(t, srv, sess, "msg", "inbox", "pane-2", "--unread", "--format", "json")
	if afterAck.cmdErr != "" {
		t.Fatalf("msg inbox after ack error: %s", afterAck.cmdErr)
	}
	if got := parseMsgCommandInboxJSON(t, afterAck.output); len(got) != 0 {
		t.Fatalf("unread inbox after read+ack = %#v, want empty", got)
	}
}

func TestMsgCommandDefaultsToActorPane(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := setupMsgCommandSession(t)
	defer cleanup()

	sent := runTestCommandWithActor(t, srv, sess, 1, "msg", "send", "--to", "pane-2", "--subject", "Actor", "--body", "actor body", "--format", "json")
	if sent.cmdErr != "" {
		t.Fatalf("msg send from actor error: %s", sent.cmdErr)
	}
	msg := parseMsgCommandSendJSON(t, sent.output)

	inbox := runTestCommandWithActor(t, srv, sess, 2, "msg", "inbox", "--unread", "--format", "json")
	if inbox.cmdErr != "" {
		t.Fatalf("msg inbox actor default error: %s", inbox.cmdErr)
	}
	if got := parseMsgCommandInboxJSON(t, inbox.output); len(got) != 1 || got[0].ID != msg.ID {
		t.Fatalf("actor-default inbox = %#v, want message %s", got, msg.ID)
	}

	read := runTestCommandWithActor(t, srv, sess, 2, "msg", "read", msg.ID)
	if read.cmdErr != "" {
		t.Fatalf("msg read actor default error: %s", read.cmdErr)
	}
	if !strings.Contains(read.output, "actor body") {
		t.Fatalf("actor-default read output = %q, want body", read.output)
	}
}

func TestMsgCommandErrorsFailLoudly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "missing recipient",
			args: []string{"send", "--from", "pane-1", "--subject", "Nope", "--body", "body"},
			want: "recipient",
		},
		{
			name: "unknown recipient",
			args: []string{"send", "--from", "pane-1", "--to", "missing", "--subject", "Nope", "--body", "body"},
			want: "not found",
		},
		{
			name: "ambiguous recipient",
			args: []string{"send", "--from", "pane-1", "--to", "shared", "--subject", "Nope", "--body", "body"},
			want: "ambiguous",
		},
		{
			name: "empty body",
			args: []string{"send", "--from", "pane-1", "--to", "pane-2", "--subject", "Nope", "--body", ""},
			want: "body",
		},
		{
			name: "invalid message ID",
			args: []string{"read", "msg-999999", "--for", "pane-2"},
			want: "not found",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, sess, cleanup := setupMsgCommandSession(t)
			defer cleanup()

			res := runTestCommand(t, srv, sess, "msg", tt.args...)
			if res.cmdErr == "" {
				t.Fatalf("msg %s succeeded, want error containing %q", strings.Join(tt.args, " "), tt.want)
			}
			if !strings.Contains(res.cmdErr, tt.want) {
				t.Fatalf("msg %s error = %q, want substring %q", strings.Join(tt.args, " "), res.cmdErr, tt.want)
			}
		})
	}
}
