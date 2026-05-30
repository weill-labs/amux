package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/weill-labs/amux/internal/mux"
)

type msgCommandSendJSON struct {
	ID         string   `json:"id"`
	Subject    string   `json:"subject"`
	Topics     []string `json:"topics"`
	Groups     []string `json:"groups"`
	ThreadID   string   `json:"thread_id"`
	InReplyTo  string   `json:"in_reply_to"`
	Recipients []struct {
		ID   uint32 `json:"id"`
		Name string `json:"name"`
	} `json:"recipients"`
}

type msgCommandInboxJSON []struct {
	ID        string `json:"id"`
	Subject   string `json:"subject"`
	ReadAt    string `json:"read_at"`
	AckedAt   string `json:"acked_at"`
	AckStatus string `json:"ack_status"`
	AckNote   string `json:"ack_note"`
	BodySize  int    `json:"body_size"`
	PartCount int    `json:"part_count"`
}

type msgCommandReadJSON struct {
	ID       string                     `json:"id"`
	Body     string                     `json:"body"`
	ReadAt   string                     `json:"read_at"`
	Metadata map[string]json.RawMessage `json:"metadata"`
	Delivery struct {
		AckStatus string `json:"ack_status"`
		AckNote   string `json:"ack_note"`
	} `json:"delivery"`
}

type msgCommandAckJSON struct {
	ID       string `json:"id"`
	Delivery struct {
		AckStatus string `json:"ack_status"`
		AckNote   string `json:"ack_note"`
	} `json:"delivery"`
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

func parseMsgCommandReadJSON(t *testing.T, raw string) msgCommandReadJSON {
	t.Helper()

	var out msgCommandReadJSON
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("json.Unmarshal(read output): %v\nraw:\n%s", err, raw)
	}
	return out
}

func parseMsgCommandAckJSON(t *testing.T, raw string) msgCommandAckJSON {
	t.Helper()

	var out msgCommandAckJSON
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("json.Unmarshal(ack output): %v\nraw:\n%s", err, raw)
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

func TestMsgCommandReplyInfersRecipientAndThread(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := setupMsgCommandSession(t)
	defer cleanup()

	root := runTestCommand(t, srv, sess, "msg", "send",
		"--from", "pane-1",
		"--to", "pane-2",
		"--subject", "Proof",
		"--topic", "handoff",
		"--group", "agents",
		"--body", "root body",
		"--format", "json",
	)
	if root.cmdErr != "" {
		t.Fatalf("msg send root error: %s", root.cmdErr)
	}
	rootJSON := parseMsgCommandSendJSON(t, root.output)

	reply := runTestCommandWithActor(t, srv, sess, 2, "msg", "reply", rootJSON.ID, "--body", "reply body", "--format", "json")
	if reply.cmdErr != "" {
		t.Fatalf("msg reply error: %s", reply.cmdErr)
	}
	replyJSON := parseMsgCommandSendJSON(t, reply.output)
	if replyJSON.InReplyTo != rootJSON.ID || replyJSON.ThreadID != rootJSON.ID {
		t.Fatalf("reply JSON = %#v, want reply in root thread", replyJSON)
	}
	if len(replyJSON.Recipients) != 1 || replyJSON.Recipients[0].Name != "pane-1" {
		t.Fatalf("reply recipients = %#v, want original sender pane-1", replyJSON.Recipients)
	}
	if got := strings.Join(replyJSON.Topics, ","); got != "handoff" {
		t.Fatalf("reply topics = %q, want inherited handoff", got)
	}
	if got := strings.Join(replyJSON.Groups, ","); got != "agents" {
		t.Fatalf("reply groups = %q, want inherited agents", got)
	}

	read := runTestCommand(t, srv, sess, "msg", "read", replyJSON.ID, "--for", "pane-1")
	if read.cmdErr != "" {
		t.Fatalf("msg read reply error: %s", read.cmdErr)
	}
	if read.output != "reply body\n" {
		t.Fatalf("reply body = %q, want reply body", read.output)
	}
}

func TestMsgCommandReplyCanAckParent(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := setupMsgCommandSession(t)
	defer cleanup()

	root := runTestCommand(t, srv, sess, "msg", "send", "--from", "pane-1", "--to", "pane-2", "--body", "please ack", "--format", "json")
	if root.cmdErr != "" {
		t.Fatalf("msg send root error: %s", root.cmdErr)
	}
	rootJSON := parseMsgCommandSendJSON(t, root.output)

	reply := runTestCommandWithActor(t, srv, sess, 2, "msg", "reply", rootJSON.ID, "--body", "acked", "--ack", "ok", "--ack-note", "handled", "--format", "json")
	if reply.cmdErr != "" {
		t.Fatalf("msg reply --ack error: %s", reply.cmdErr)
	}

	inbox := runTestCommand(t, srv, sess, "msg", "inbox", "pane-2", "--format", "json")
	if inbox.cmdErr != "" {
		t.Fatalf("msg inbox error: %s", inbox.cmdErr)
	}
	all := parseMsgCommandInboxJSON(t, inbox.output)
	if len(all) != 1 || all[0].AckStatus != "ok" || all[0].AckNote != "handled" {
		t.Fatalf("pane-2 inbox = %#v, want parent acked ok with note", all)
	}
}

func TestMsgCommandReplyRecipientInferenceEdges(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := setupMsgCommandSession(t)
	defer cleanup()

	root := runTestCommand(t, srv, sess, "msg", "send",
		"--from", "pane-1",
		"--to", "pane-2",
		"--topic", "root-topic",
		"--group", "root-group",
		"--body", "root",
		"--format", "json",
	)
	if root.cmdErr != "" {
		t.Fatalf("msg send root error: %s", root.cmdErr)
	}
	rootJSON := parseMsgCommandSendJSON(t, root.output)

	fromOriginalSender := runTestCommandWithActor(t, srv, sess, 1, "msg", "reply", rootJSON.ID, "--body", "sender follow-up", "--format", "json")
	if fromOriginalSender.cmdErr != "" {
		t.Fatalf("msg reply from original sender error: %s", fromOriginalSender.cmdErr)
	}
	fromOriginalSenderJSON := parseMsgCommandSendJSON(t, fromOriginalSender.output)
	if len(fromOriginalSenderJSON.Recipients) != 1 || fromOriginalSenderJSON.Recipients[0].Name != "pane-2" {
		t.Fatalf("original sender reply recipients = %#v, want original recipient pane-2", fromOriginalSenderJSON.Recipients)
	}

	explicitTo := runTestCommandWithActor(t, srv, sess, 3, "msg", "reply", rootJSON.ID,
		"--to", "pane-1",
		"--topic", "override-topic",
		"--group", "override-group",
		"--body", "observer reply",
		"--format", "json",
	)
	if explicitTo.cmdErr != "" {
		t.Fatalf("msg reply with explicit recipient error: %s", explicitTo.cmdErr)
	}
	explicitToJSON := parseMsgCommandSendJSON(t, explicitTo.output)
	if len(explicitToJSON.Recipients) != 1 || explicitToJSON.Recipients[0].Name != "pane-1" {
		t.Fatalf("explicit reply recipients = %#v, want pane-1", explicitToJSON.Recipients)
	}
	if got := strings.Join(explicitToJSON.Topics, ","); got != "override-topic" {
		t.Fatalf("explicit reply topics = %q, want override-topic", got)
	}
	if got := strings.Join(explicitToJSON.Groups, ","); got != "override-group" {
		t.Fatalf("explicit reply groups = %q, want override-group", got)
	}

	observer := runTestCommandWithActor(t, srv, sess, 3, "msg", "reply", rootJSON.ID, "--body", "cannot infer")
	if observer.cmdErr == "" || !strings.Contains(observer.cmdErr, "pass --to") {
		t.Fatalf("observer reply error = %q, want pass --to", observer.cmdErr)
	}

	self := runTestCommand(t, srv, sess, "msg", "send", "--from", "pane-1", "--to", "pane-1", "--body", "self", "--format", "json")
	if self.cmdErr != "" {
		t.Fatalf("msg send self error: %s", self.cmdErr)
	}
	selfJSON := parseMsgCommandSendJSON(t, self.output)
	selfReply := runTestCommandWithActor(t, srv, sess, 1, "msg", "reply", selfJSON.ID, "--body", "cannot infer")
	if selfReply.cmdErr == "" || !strings.Contains(selfReply.cmdErr, "pass --to") {
		t.Fatalf("self reply error = %q, want pass --to", selfReply.cmdErr)
	}
}

func TestMsgCommandTextAndDetailedJSON(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := setupMsgCommandSession(t)
	defer cleanup()

	first := runTestCommand(t, srv, sess, "msg", "send",
		"--from", "pane-1",
		"--to", "pane-2,3",
		"--subject", "Thread root",
		"--topic", "build,review",
		"--group", "agents",
		"--metadata", `{"priority":"high"}`,
		"--body", "root body",
	)
	if first.cmdErr != "" {
		t.Fatalf("msg send text error: %s", first.cmdErr)
	}
	if !strings.Contains(first.output, "Sent msg-000001 to pane-2,shared") {
		t.Fatalf("msg send text output = %q, want recipients", first.output)
	}

	inboxText := runTestCommand(t, srv, sess, "msg", "inbox", "pane-2")
	if inboxText.cmdErr != "" {
		t.Fatalf("msg inbox text error: %s", inboxText.cmdErr)
	}
	if !strings.Contains(inboxText.output, "msg-000001 from pane-1: Thread root (9 bytes)") {
		t.Fatalf("msg inbox text output = %q, want summary", inboxText.output)
	}

	peek := runTestCommand(t, srv, sess, "msg", "read", "msg-000001", "--for", "pane-2", "--peek", "--format", "json")
	if peek.cmdErr != "" {
		t.Fatalf("msg read peek JSON error: %s", peek.cmdErr)
	}
	peekJSON := parseMsgCommandReadJSON(t, peek.output)
	if peekJSON.Body != "root body" || peekJSON.ReadAt != "" {
		t.Fatalf("peek JSON = %#v, want body without read_at", peekJSON)
	}
	if got := string(peekJSON.Metadata["priority"]); got != `"high"` {
		t.Fatalf("metadata priority = %s, want high", got)
	}

	read := runTestCommand(t, srv, sess, "msg", "read", "msg-000001", "--for", "pane-2")
	if read.cmdErr != "" {
		t.Fatalf("msg read text error: %s", read.cmdErr)
	}
	if read.output != "root body\n" {
		t.Fatalf("msg read text output = %q, want body with trailing newline", read.output)
	}

	ackText := runTestCommand(t, srv, sess, "msg", "ack", "msg-000001", "--for", "pane-2")
	if ackText.cmdErr != "" {
		t.Fatalf("msg ack text error: %s", ackText.cmdErr)
	}
	if ackText.output != "Acked msg-000001 for pane-2\n" {
		t.Fatalf("msg ack text output = %q, want no-status ack", ackText.output)
	}

	allInbox := runTestCommand(t, srv, sess, "msg", "inbox", "pane-2", "--format", "json")
	if allInbox.cmdErr != "" {
		t.Fatalf("msg inbox all JSON error: %s", allInbox.cmdErr)
	}
	all := parseMsgCommandInboxJSON(t, allInbox.output)
	if len(all) != 1 || all[0].ReadAt == "" || all[0].AckedAt == "" {
		t.Fatalf("all inbox JSON = %#v, want read and ack timestamps", all)
	}

	reply := runTestCommand(t, srv, sess, "msg", "send",
		"--from", "pane-2",
		"--to", "pane-1",
		"--reply-to", "msg-000001",
		"--body", "reply body",
		"--format", "json",
	)
	if reply.cmdErr != "" {
		t.Fatalf("msg reply send error: %s", reply.cmdErr)
	}
	replyJSON := parseMsgCommandSendJSON(t, reply.output)
	if replyJSON.InReplyTo != "msg-000001" || replyJSON.ThreadID != "msg-000001" {
		t.Fatalf("reply JSON = %#v, want reply in root thread", replyJSON)
	}

	ackJSONRaw := runTestCommand(t, srv, sess, "msg", "ack", replyJSON.ID, "--for", "pane-1", "--status", "seen", "--note", "queued", "--format", "json")
	if ackJSONRaw.cmdErr != "" {
		t.Fatalf("msg ack JSON error: %s", ackJSONRaw.cmdErr)
	}
	ackJSON := parseMsgCommandAckJSON(t, ackJSONRaw.output)
	if ackJSON.ID != replyJSON.ID || ackJSON.Delivery.AckStatus != "seen" || ackJSON.Delivery.AckNote != "queued" {
		t.Fatalf("ack JSON = %#v, want seen note", ackJSON)
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
		{
			name: "missing subcommand",
			args: nil,
			want: "usage: msg",
		},
		{
			name: "unknown subcommand",
			args: []string{"wait"},
			want: "usage: msg",
		},
		{
			name: "missing actor default",
			args: []string{"inbox"},
			want: "inbox target pane is required",
		},
		{
			name: "unsupported format",
			args: []string{"send", "--from", "pane-1", "--to", "pane-2", "--body", "body", "--format", "yaml"},
			want: "unsupported msg format",
		},
		{
			name: "invalid metadata",
			args: []string{"send", "--from", "pane-1", "--to", "pane-2", "--body", "body", "--metadata", "null"},
			want: "metadata must be a JSON object",
		},
		{
			name: "unknown send flag",
			args: []string{"send", "--unknown"},
			want: "usage: msg send",
		},
		{
			name: "unknown inbox flag",
			args: []string{"inbox", "--bad"},
			want: "usage: msg inbox",
		},
		{
			name: "duplicate inbox target",
			args: []string{"inbox", "pane-1", "pane-2"},
			want: "usage: msg inbox",
		},
		{
			name: "missing read id",
			args: []string{"read"},
			want: "usage: msg read",
		},
		{
			name: "missing reply id",
			args: []string{"reply"},
			want: "usage: msg reply",
		},
		{
			name: "unknown reply flag",
			args: []string{"reply", "msg-000001", "--bad"},
			want: "usage: msg reply",
		},
		{
			name: "missing reply body value",
			args: []string{"reply", "msg-000001", "--body"},
			want: "missing value for --body",
		},
		{
			name: "unknown read flag",
			args: []string{"read", "msg-000001", "--bad"},
			want: "usage: msg read",
		},
		{
			name: "missing ack id",
			args: []string{"ack"},
			want: "usage: msg ack",
		},
		{
			name: "unknown ack flag",
			args: []string{"ack", "msg-000001", "--bad"},
			want: "usage: msg ack",
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
