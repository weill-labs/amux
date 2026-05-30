package server

import (
	"encoding/json"
	"fmt"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	caputil "github.com/weill-labs/amux/internal/capture"
	"github.com/weill-labs/amux/internal/mailbox"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

func TestCaptureDefaultSinglePaneDoesNotSendClientCaptureRequest(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()
	sess.captureTiming.responseTimeout = 20 * time.Millisecond

	pane := newStandaloneProxyPane(1, "pane-1")
	pane.FeedOutput([]byte("SERVER-LOCAL\r\n"))
	window := newTestWindowWithPanes(t, sess, 1, "main", pane)
	setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, pane)

	captureClient := attachCaptureClientForCommandTest(t, sess)

	res := runTestCommand(t, srv, sess, "capture", "pane-1")
	if res.cmdErr != "" {
		t.Fatalf("capture cmdErr = %q, want empty", res.cmdErr)
	}
	if got, want := res.output, "SERVER-LOCAL\n"; got != want {
		t.Fatalf("capture output = %q, want %q", got, want)
	}

	if err := captureClient.SetReadDeadline(time.Now().Add(25 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if msg, err := readMsgOnConn(captureClient); err == nil {
		t.Fatalf("attached client received %v, want no client capture request", msg.Type)
	}
}

func TestCaptureDefaultSinglePaneJSONDoesNotSendClientCaptureRequest(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()
	sess.captureTiming.responseTimeout = 20 * time.Millisecond

	pane := newStandaloneProxyPane(1, "pane-1")
	pane.FeedOutput([]byte("SERVER-JSON\r\n"))
	window := newTestWindowWithPanes(t, sess, 1, "main", pane)
	setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, pane)

	captureClient := attachCaptureClientForCommandTest(t, sess)

	res := runTestCommand(t, srv, sess, "capture", "--format", "json", "pane-1")
	if res.cmdErr != "" {
		t.Fatalf("capture cmdErr = %q, want empty", res.cmdErr)
	}
	var capturePane struct {
		Name    string   `json:"name"`
		Content []string `json:"content"`
	}
	if err := json.Unmarshal([]byte(res.output), &capturePane); err != nil {
		t.Fatalf("json.Unmarshal capture output: %v\n%s", err, res.output)
	}
	if capturePane.Name != "pane-1" {
		t.Fatalf("capture pane name = %q, want pane-1", capturePane.Name)
	}
	if len(capturePane.Content) == 0 || capturePane.Content[0] != "SERVER-JSON" {
		t.Fatalf("capture content = %#v, want SERVER-JSON as first row", capturePane.Content)
	}

	if err := captureClient.SetReadDeadline(time.Now().Add(25 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if msg, err := readMsgOnConn(captureClient); err == nil {
		t.Fatalf("attached client received %v, want no client capture request", msg.Type)
	}
}

func TestCaptureJSONMailboxZeroUnread(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newMailboxCaptureSession(t, 1)
	defer cleanup()

	res := runTestCommand(t, srv, sess, "capture", "--format", "json", "pane-1")
	if res.cmdErr != "" {
		t.Fatalf("capture cmdErr = %q, want empty", res.cmdErr)
	}

	pane := decodeCapturePaneMap(t, res.output)
	mailbox := mailboxMap(t, pane)
	assertJSONNumber(t, mailbox, "unread", 0)
	assertJSONArrayLen(t, mailbox, "latest_unread", 0)
	assertJSONMapLen(t, mailbox, "topics", 0)
}

func TestCaptureJSONMailboxOmitsBodiesAndMetadata(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newMailboxCaptureSession(t, 2)
	defer cleanup()

	seedMailboxMessage(t, sess, "pane-1", []string{"pane-2"}, mailbox.SendRequest{
		Subject: "Review ready",
		Body:    []byte("FULL_BODY_SECRET_DO_NOT_CAPTURE"),
		Topics:  []string{"review"},
		Groups:  []string{"backend"},
		Metadata: map[string]json.RawMessage{
			"priority": json.RawMessage(`"METADATA_SECRET_DO_NOT_CAPTURE"`),
		},
	})

	res := runTestCommand(t, srv, sess, "capture", "--format", "json")
	if res.cmdErr != "" {
		t.Fatalf("capture cmdErr = %q, want empty", res.cmdErr)
	}
	if strings.Contains(res.output, "FULL_BODY_SECRET_DO_NOT_CAPTURE") {
		t.Fatalf("capture output leaked message body:\n%s", res.output)
	}
	if strings.Contains(res.output, "METADATA_SECRET_DO_NOT_CAPTURE") || strings.Contains(res.output, "metadata") {
		t.Fatalf("capture output leaked message metadata:\n%s", res.output)
	}

	capture := decodeCaptureMap(t, res.output)
	pane1 := capturePaneMap(t, capture, "pane-1")
	assertJSONNumber(t, mailboxMap(t, pane1), "unread", 0)

	pane2 := capturePaneMap(t, capture, "pane-2")
	mailbox := mailboxMap(t, pane2)
	assertJSONNumber(t, mailbox, "unread", 1)
	assertTopicCount(t, mailbox, "review", 1)

	latest := latestUnread(t, mailbox)
	if len(latest) != 1 {
		t.Fatalf("latest_unread length = %d, want 1", len(latest))
	}
	summary := latest[0]
	assertJSONString(t, summary, "id", "msg-000001")
	assertJSONString(t, summary, "subject", "Review ready")
	assertJSONString(t, summary, "thread_id", "msg-000001")
	assertJSONNumber(t, summary, "body_size", len("FULL_BODY_SECRET_DO_NOT_CAPTURE"))
	assertJSONNumber(t, summary, "part_count", 1)
	assertStringSlice(t, summary["topics"], []string{"review"})
	assertStringSlice(t, summary["groups"], []string{"backend"})

	from := jsonObject(t, summary, "from")
	assertJSONNumber(t, from, "id", 1)
	assertJSONString(t, from, "name", "pane-1")
	assertJSONString(t, from, "host", mux.DefaultHost)

	for _, forbidden := range []string{"body", "parts", "bytes", "metadata", "read_at", "acked_at", "ack_status", "ack_note"} {
		if _, ok := summary[forbidden]; ok {
			t.Fatalf("latest unread summary includes %q: %#v", forbidden, summary)
		}
	}
}

func TestCaptureJSONMailboxLatestUnreadLimitAndTopicCounts(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newMailboxCaptureSession(t, 2)
	defer cleanup()

	for i := 1; i <= 6; i++ {
		topic := "status"
		if i%2 == 0 {
			topic = "review"
		}
		seedMailboxMessage(t, sess, "pane-1", []string{"pane-2"}, mailbox.SendRequest{
			Subject: fmt.Sprintf("message %d", i),
			Body:    []byte(fmt.Sprintf("body-%d", i)),
			Topics:  []string{topic},
		})
	}

	res := runTestCommand(t, srv, sess, "capture", "--format", "json", "pane-2")
	if res.cmdErr != "" {
		t.Fatalf("capture cmdErr = %q, want empty", res.cmdErr)
	}

	pane := decodeCapturePaneMap(t, res.output)
	mailbox := mailboxMap(t, pane)
	assertJSONNumber(t, mailbox, "unread", 6)
	assertTopicCount(t, mailbox, "review", 3)
	assertTopicCount(t, mailbox, "status", 3)

	latest := latestUnread(t, mailbox)
	if len(latest) != 5 {
		t.Fatalf("latest_unread length = %d, want fixed limit 5", len(latest))
	}
	for i, wantID := range []string{"msg-000006", "msg-000005", "msg-000004", "msg-000003", "msg-000002"} {
		assertJSONString(t, latest[i], "id", wantID)
	}
}

func TestCaptureJSONMailboxMultiplePanesAndReadAckState(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newMailboxCaptureSession(t, 3)
	defer cleanup()

	msg1 := seedMailboxMessage(t, sess, "pane-1", []string{"pane-2", "pane-3"}, mailbox.SendRequest{
		Subject: "fanout",
		Body:    []byte("shared"),
		Topics:  []string{"review"},
	})
	msg2 := seedMailboxMessage(t, sess, "pane-1", []string{"pane-3"}, mailbox.SendRequest{
		Subject: "direct",
		Body:    []byte("direct"),
		Topics:  []string{"status"},
	})

	mustSessionMutation(t, sess, func(sess *Session) {
		if _, _, err := sess.ensureMailbox().Read(msg1.ID, 2, mailbox.ReadOptions{}); err != nil {
			t.Fatalf("Read pane-2 msg1: %v", err)
		}
		if _, err := sess.ensureMailbox().Ack(msg2.ID, 3, mailbox.AckRequest{Status: "seen"}); err != nil {
			t.Fatalf("Ack pane-3 msg2: %v", err)
		}
	})

	res := runTestCommand(t, srv, sess, "capture", "--format", "json")
	if res.cmdErr != "" {
		t.Fatalf("capture cmdErr = %q, want empty", res.cmdErr)
	}

	capture := decodeCaptureMap(t, res.output)
	assertJSONNumber(t, mailboxMap(t, capturePaneMap(t, capture, "pane-1")), "unread", 0)
	assertJSONNumber(t, mailboxMap(t, capturePaneMap(t, capture, "pane-2")), "unread", 0)

	pane3Mailbox := mailboxMap(t, capturePaneMap(t, capture, "pane-3"))
	assertJSONNumber(t, pane3Mailbox, "unread", 2)
	assertTopicCount(t, pane3Mailbox, "review", 1)
	assertTopicCount(t, pane3Mailbox, "status", 1)
	latest := latestUnread(t, pane3Mailbox)
	if len(latest) != 2 {
		t.Fatalf("pane-3 latest_unread length = %d, want 2", len(latest))
	}
	assertJSONString(t, latest[0], "id", string(msg2.ID))
	assertJSONString(t, latest[1], "id", string(msg1.ID))
	for _, summary := range latest {
		if _, ok := summary["acked_at"]; ok {
			t.Fatalf("capture summary leaked ack state: %#v", summary)
		}
	}
}

func TestCaptureDefaultSinglePaneWorksWithoutAttachedClient(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	pane := newStandaloneProxyPane(1, "pane-1")
	pane.FeedOutput([]byte("SERVER-LOCAL\r\n"))
	window := newTestWindowWithPanes(t, sess, 1, "main", pane)
	setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, pane)

	res := runTestCommand(t, srv, sess, "capture", "pane-1")
	if res.cmdErr != "" {
		t.Fatalf("capture cmdErr = %q, want empty", res.cmdErr)
	}
	if got, want := res.output, "SERVER-LOCAL\n"; got != want {
		t.Fatalf("capture output = %q, want %q", got, want)
	}
}

func TestCaptureFullSessionTextUsesServerLayoutWithoutAttachedClient(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()
	sess.captureTiming.attachMaxRetries = 1

	pane1 := newStandaloneProxyPane(1, "pane-1")
	pane2 := newStandaloneProxyPane(2, "pane-2")
	pane1.FeedOutput([]byte("LEFT-SERVER\r\n"))
	pane2.FeedOutput([]byte("RIGHT-SERVER\r\n"))
	window := newTestWindowWithPanes(t, sess, 1, "main", pane1, pane2)
	setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, pane1, pane2)

	res := runTestCommand(t, srv, sess, "capture")
	if res.cmdErr != "" {
		t.Fatalf("capture cmdErr = %q, want empty", res.cmdErr)
	}
	for _, want := range []string{"LEFT-SERVER", "RIGHT-SERVER", "[pane-1]", "[pane-2]", "test-command-queue"} {
		if !strings.Contains(res.output, want) {
			t.Fatalf("capture output missing %q:\n%s", want, res.output)
		}
	}
}

func TestCaptureFullSessionJSONUsesServerPaneMetadataWithoutAttachedClient(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()
	sess.captureTiming.attachMaxRetries = 1

	pane1 := newStandaloneProxyPane(1, "pane-1")
	pane2 := newStandaloneProxyPane(2, "pane-2")
	pane1.FeedOutput([]byte("JSON-SERVER-ONE\r\n"))
	pane2.FeedOutput([]byte("JSON-SERVER-TWO\r\n"))
	pane1.Meta.Task = "ship"
	pane1.Meta.GitBranch = "feature/full-session-capture"
	pane1.Meta.PR = "1760"
	pane1.Meta.KV = map[string]string{"issue": "LAB-1760", "owner": "codex"}
	pane1.Meta.TrackedPRs = []proto.TrackedPR{{Number: 1760, Status: proto.TrackedStatusActive}}
	pane1.Meta.TrackedIssues = []proto.TrackedIssue{{ID: "LAB-1760", Status: proto.TrackedStatusActive}}
	window := newTestWindowWithPanes(t, sess, 1, "main", pane1, pane2)
	setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, pane1, pane2)

	res := runTestCommand(t, srv, sess, "capture", "--format", "json")
	if res.cmdErr != "" {
		t.Fatalf("capture cmdErr = %q, want empty", res.cmdErr)
	}
	var capture proto.CaptureJSON
	if err := json.Unmarshal([]byte(res.output), &capture); err != nil {
		t.Fatalf("json.Unmarshal capture output: %v\n%s", err, res.output)
	}
	if capture.Error != nil {
		t.Fatalf("capture error = %+v, want nil", capture.Error)
	}
	if capture.Session != "test-command-queue" {
		t.Fatalf("capture session = %q, want test-command-queue", capture.Session)
	}
	if capture.Window.ID != 1 || capture.Window.Name != "main" || capture.Window.Index != 1 {
		t.Fatalf("capture window = %+v, want active main window", capture.Window)
	}
	if len(capture.Panes) != 2 {
		t.Fatalf("capture panes = %d, want 2: %+v", len(capture.Panes), capture.Panes)
	}

	var pane *proto.CapturePane
	for i := range capture.Panes {
		if capture.Panes[i].Position == nil {
			t.Fatalf("pane %s missing position", capture.Panes[i].Name)
		}
		if capture.Panes[i].Name == "pane-1" {
			pane = &capture.Panes[i]
		}
	}
	if pane == nil {
		t.Fatalf("pane-1 missing from capture: %+v", capture.Panes)
	}
	if pane.Task != "ship" || pane.Meta.Task != "ship" {
		t.Fatalf("pane task fields = top-level %q meta %q, want ship", pane.Task, pane.Meta.Task)
	}
	if pane.GitBranch != "feature/full-session-capture" || pane.Meta.GitBranch != "feature/full-session-capture" {
		t.Fatalf("pane git branch fields = top-level %q meta %q", pane.GitBranch, pane.Meta.GitBranch)
	}
	if pane.PR != "1760" || pane.Meta.PR != "1760" {
		t.Fatalf("pane PR fields = top-level %q meta %q", pane.PR, pane.Meta.PR)
	}
	if got := pane.Meta.KV["issue"]; got != "LAB-1760" {
		t.Fatalf("pane meta issue = %q, want LAB-1760", got)
	}
	if len(pane.Meta.TrackedPRs) != 1 || pane.Meta.TrackedPRs[0].Number != 1760 {
		t.Fatalf("pane tracked PRs = %+v, want PR 1760", pane.Meta.TrackedPRs)
	}
	if len(pane.Meta.TrackedIssues) != 1 || pane.Meta.TrackedIssues[0].ID != "LAB-1760" {
		t.Fatalf("pane tracked issues = %+v, want LAB-1760", pane.Meta.TrackedIssues)
	}
	if joined := strings.Join(pane.Content, "\n"); !strings.Contains(joined, "JSON-SERVER-ONE") {
		t.Fatalf("pane content = %q, want JSON-SERVER-ONE", joined)
	}
}

func TestCaptureFullSessionHistoryJSONSeparatesScrollback(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()
	sess.captureTiming.attachMaxRetries = 1

	pane := newStandaloneProxyPane(1, "pane-1")
	var output strings.Builder
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(&output, "SERVER-HISTORY-%02d\r\n", i)
	}
	pane.FeedOutput([]byte(output.String()))
	window := newTestWindowWithPanes(t, sess, 1, "main", pane)
	setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, pane)

	res := runTestCommand(t, srv, sess, "capture", "--history", "--format", "json")
	if res.cmdErr != "" {
		t.Fatalf("capture cmdErr = %q, want empty", res.cmdErr)
	}
	var capture proto.CaptureJSON
	if err := json.Unmarshal([]byte(res.output), &capture); err != nil {
		t.Fatalf("json.Unmarshal capture output: %v\n%s", err, res.output)
	}
	if len(capture.Panes) != 1 {
		t.Fatalf("capture panes = %d, want 1: %+v", len(capture.Panes), capture.Panes)
	}
	paneCapture := capture.Panes[0]
	if got := strings.Join(paneCapture.History, "\n"); !strings.Contains(got, "SERVER-HISTORY-01") {
		t.Fatalf("pane history = %q, want SERVER-HISTORY-01; full pane: %+v", got, paneCapture)
	}
	if got := strings.Join(paneCapture.Content, "\n"); strings.Contains(got, "SERVER-HISTORY-01") {
		t.Fatalf("pane content should not contain scrollback history, got %q", got)
	}
	if got := strings.Join(paneCapture.Content, "\n"); !strings.Contains(got, "SERVER-HISTORY-30") {
		t.Fatalf("pane content = %q, want SERVER-HISTORY-30", got)
	}
}

func TestCaptureClientFlagForwardsToAttachedClient(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	pane := newStandaloneProxyPane(1, "pane-1")
	pane.FeedOutput([]byte("SERVER-LOCAL\r\n"))
	window := newTestWindowWithPanes(t, sess, 1, "main", pane)
	setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, pane)

	captureClient := attachCaptureClientForCommandTest(t, sess)
	requestCh, errCh := respondToNextCaptureRequest(t, sess, captureClient, "CLIENT-PANE\n")

	res := runTestCommand(t, srv, sess, "capture", "--client", "pane-1")
	if res.cmdErr != "" {
		t.Fatalf("capture cmdErr = %q, want empty", res.cmdErr)
	}
	if got, want := res.output, "CLIENT-PANE\n"; got != want {
		t.Fatalf("capture output = %q, want %q", got, want)
	}

	select {
	case err := <-errCh:
		t.Fatalf("reading capture request: %v", err)
	case msg := <-requestCh:
		if msg.Type != MsgTypeCaptureRequest {
			t.Fatalf("message type = %v, want %v", msg.Type, MsgTypeCaptureRequest)
		}
		if want := []string{"--client", "pane-1"}; !slices.Equal(msg.CmdArgs, want) {
			t.Fatalf("capture request args = %v, want %v", msg.CmdArgs, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for forwarded capture request")
	}
}

func TestCaptureClientFlagForwardsFullSessionToAttachedClient(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	pane := newStandaloneProxyPane(1, "pane-1")
	pane.FeedOutput([]byte("SERVER-LOCAL\r\n"))
	window := newTestWindowWithPanes(t, sess, 1, "main", pane)
	setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, pane)

	captureClient := attachCaptureClientForCommandTest(t, sess)
	requestCh, errCh := respondToNextCaptureRequest(t, sess, captureClient, "CLIENT-FULL\n")

	res := runTestCommand(t, srv, sess, "capture", "--client")
	if res.cmdErr != "" {
		t.Fatalf("capture cmdErr = %q, want empty", res.cmdErr)
	}
	if got, want := res.output, "CLIENT-FULL\n"; got != want {
		t.Fatalf("capture output = %q, want %q", got, want)
	}

	select {
	case err := <-errCh:
		t.Fatalf("reading capture request: %v", err)
	case msg := <-requestCh:
		if msg.Type != MsgTypeCaptureRequest {
			t.Fatalf("message type = %v, want %v", msg.Type, MsgTypeCaptureRequest)
		}
		if want := []string{"--client"}; !slices.Equal(msg.CmdArgs, want) {
			t.Fatalf("capture request args = %v, want %v", msg.CmdArgs, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for forwarded capture request")
	}
}

func TestCaptureLegacyClientEnvForwardsSinglePaneToAttachedClient(t *testing.T) {
	t.Setenv("AMUX_CAPTURE_LEGACY_CLIENT", "1")

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	pane := newStandaloneProxyPane(1, "pane-1")
	pane.FeedOutput([]byte("SERVER-LOCAL\r\n"))
	window := newTestWindowWithPanes(t, sess, 1, "main", pane)
	setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, pane)

	captureClient := attachCaptureClientForCommandTest(t, sess)
	requestCh, errCh := respondToNextCaptureRequest(t, sess, captureClient, "CLIENT-PANE\n")

	res := runTestCommand(t, srv, sess, "capture", "pane-1")
	if res.cmdErr != "" {
		t.Fatalf("capture cmdErr = %q, want empty", res.cmdErr)
	}
	if got, want := res.output, "CLIENT-PANE\n"; got != want {
		t.Fatalf("capture output = %q, want %q", got, want)
	}

	select {
	case err := <-errCh:
		t.Fatalf("reading capture request: %v", err)
	case msg := <-requestCh:
		if msg.Type != MsgTypeCaptureRequest {
			t.Fatalf("message type = %v, want %v", msg.Type, MsgTypeCaptureRequest)
		}
		if want := []string{"1"}; !slices.Equal(msg.CmdArgs, want) {
			t.Fatalf("capture request args = %v, want %v", msg.CmdArgs, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for forwarded capture request")
	}
}

func TestCaptureLegacyClientEnvForwardsFullSessionToAttachedClient(t *testing.T) {
	t.Setenv("AMUX_CAPTURE_LEGACY_CLIENT", "1")

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	pane := newStandaloneProxyPane(1, "pane-1")
	pane.FeedOutput([]byte("SERVER-LOCAL\r\n"))
	window := newTestWindowWithPanes(t, sess, 1, "main", pane)
	setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, pane)

	captureClient := attachCaptureClientForCommandTest(t, sess)
	requestCh, errCh := respondToNextCaptureRequest(t, sess, captureClient, "CLIENT-FULL\n")

	res := runTestCommand(t, srv, sess, "capture")
	if res.cmdErr != "" {
		t.Fatalf("capture cmdErr = %q, want empty", res.cmdErr)
	}
	if got, want := res.output, "CLIENT-FULL\n"; got != want {
		t.Fatalf("capture output = %q, want %q", got, want)
	}

	select {
	case err := <-errCh:
		t.Fatalf("reading capture request: %v", err)
	case msg := <-requestCh:
		if msg.Type != MsgTypeCaptureRequest {
			t.Fatalf("message type = %v, want %v", msg.Type, MsgTypeCaptureRequest)
		}
		if len(msg.CmdArgs) != 0 {
			t.Fatalf("capture request args = %v, want empty", msg.CmdArgs)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for forwarded capture request")
	}
}

func TestCaptureClientFlagRequiresAttachedClient(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()
	sess.captureTiming.attachMaxRetries = 1

	pane := newStandaloneProxyPane(1, "pane-1")
	pane.FeedOutput([]byte("SERVER-LOCAL\r\n"))
	window := newTestWindowWithPanes(t, sess, 1, "main", pane)
	setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, pane)

	res := runTestCommand(t, srv, sess, "capture", "--client", "pane-1")
	if got, want := res.cmdErr, "no client attached"; got != want {
		t.Fatalf("capture --client without client cmdErr = %q, want %q; output=%q", got, want, res.output)
	}
}

func TestCaptureFullSessionDefaultDoesNotSendClientCaptureRequest(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()
	sess.captureTiming.responseTimeout = 20 * time.Millisecond

	pane := newStandaloneProxyPane(1, "pane-1")
	pane.FeedOutput([]byte("SERVER-LOCAL\r\n"))
	window := newTestWindowWithPanes(t, sess, 1, "main", pane)
	setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, pane)

	captureClient := attachCaptureClientForCommandTest(t, sess)

	res := runTestCommand(t, srv, sess, "capture")
	if res.cmdErr != "" {
		t.Fatalf("capture cmdErr = %q, want empty", res.cmdErr)
	}
	if !strings.Contains(res.output, "SERVER-LOCAL") || !strings.Contains(res.output, "[pane-1]") {
		t.Fatalf("capture output should include server-rendered pane content, got:\n%s", res.output)
	}

	if err := captureClient.SetReadDeadline(time.Now().Add(25 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if msg, err := readMsgOnConn(captureClient); err == nil {
		t.Fatalf("attached client received %v, want no client capture request", msg.Type)
	}
}

func TestCaptureFullSessionDuringBusyOutputDoesNotRequestClientRepaint(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()
	sess.captureTiming.responseTimeout = 20 * time.Millisecond

	pane := newStandaloneProxyPane(1, "pane-1")
	window := newTestWindowWithPanes(t, sess, 1, "main", pane)
	setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, pane)
	captureClient := attachCaptureClientForCommandTest(t, sess)

	for i := 0; i < 5; i++ {
		pane.FeedOutput([]byte("BUSY-TUI-FRAME\r\n"))
		res := runTestCommand(t, srv, sess, "capture")
		if res.cmdErr != "" {
			t.Fatalf("capture #%d cmdErr = %q, want empty", i+1, res.cmdErr)
		}
		if !strings.Contains(res.output, "BUSY-TUI-FRAME") {
			t.Fatalf("capture #%d output missing busy pane content:\n%s", i+1, res.output)
		}
	}

	if err := captureClient.SetReadDeadline(time.Now().Add(25 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if msg, err := readMsgOnConn(captureClient); err == nil {
		t.Fatalf("attached client received %v, want no capture-induced repaint request", msg.Type)
	}
}

func TestCaptureHistoryPaneUsesServerHistoryPath(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	pane := newStandaloneProxyPane(1, "pane-1")
	pane.SetRetainedHistory([]string{"SERVER-HISTORY"})
	pane.FeedOutput([]byte("SERVER-VISIBLE"))
	window := newTestWindowWithPanes(t, sess, 1, "main", pane)
	setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, pane)

	captureClient := attachCaptureClientForCommandTest(t, sess)

	res := runTestCommand(t, srv, sess, "capture", "--history", "pane-1")
	if res.cmdErr != "" {
		t.Fatalf("capture cmdErr = %q, want empty", res.cmdErr)
	}
	if got, want := res.output, "SERVER-HISTORY\nSERVER-VISIBLE\n"; got != want {
		t.Fatalf("capture output = %q, want %q", got, want)
	}

	if err := captureClient.SetReadDeadline(time.Now().Add(25 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if msg, err := readMsgOnConn(captureClient); err == nil {
		t.Fatalf("attached client received %v, want no client capture request", msg.Type)
	}
}

func TestCaptureLocallyRejectsMissingSessionForFullSession(t *testing.T) {
	t.Parallel()

	res := captureLocally(&CommandContext{}, nil)
	if got, want := res.CmdErr, "no session"; got != want {
		t.Fatalf("captureLocally CmdErr = %q, want %q", got, want)
	}
}

func TestCaptureSinglePaneLocallyRejectsInvalidScreenFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  caputil.Request
		want string
	}{
		{
			name: "mutually exclusive formats",
			req: caputil.Request{
				PaneRef:     "pane-1",
				IncludeANSI: true,
				FormatJSON:  true,
			},
			want: "--ansi, --colors, and --format json are mutually exclusive",
		},
		{
			name: "pane color map",
			req: caputil.Request{
				PaneRef:  "pane-1",
				ColorMap: true,
			},
			want: "--colors is only supported for full screen capture",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			res := captureSinglePaneLocally(&CommandContext{}, tt.req)
			if got := res.CmdErr; got != tt.want {
				t.Fatalf("captureSinglePaneLocally CmdErr = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCaptureSinglePaneLocallyReturnsResolveError(t *testing.T) {
	t.Parallel()

	_, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	pane := newStandaloneProxyPane(1, "pane-1")
	window := newTestWindowWithPanes(t, sess, 1, "main", pane)
	setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, pane)

	res := captureSinglePaneLocally(&CommandContext{Sess: sess}, caputil.Request{PaneRef: "missing"})
	if got := res.CmdErr; !strings.Contains(got, "not found") {
		t.Fatalf("captureSinglePaneLocally CmdErr = %q, want pane not found", got)
	}
}

func attachCaptureClientForCommandTest(t *testing.T, sess *Session) net.Conn {
	t.Helper()

	serverConn, peerConn := net.Pipe()
	attached := newClientConn(serverConn)
	attached.ID = "client-capture"
	t.Cleanup(func() {
		attached.Close()
		_ = peerConn.Close()
		_ = serverConn.Close()
	})

	mustSessionMutation(t, sess, func(sess *Session) {
		sess.ensureClientManager().setClientsForTest(attached)
	})
	return peerConn
}

func respondToNextCaptureRequest(t *testing.T, sess *Session, conn net.Conn, output string) (<-chan *Message, <-chan error) {
	t.Helper()

	requestCh := make(chan *Message, 1)
	errCh := make(chan error, 1)
	go func() {
		if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			errCh <- err
			return
		}
		msg, err := readMsgOnConn(conn)
		if err != nil {
			errCh <- err
			return
		}
		requestCh <- msg
		sess.routeCaptureResponse(&Message{
			Type:      MsgTypeCaptureResponse,
			CmdOutput: output,
		})
	}()
	return requestCh, errCh
}

func newMailboxCaptureSession(t *testing.T, paneCount int) (*Server, *Session, func()) {
	t.Helper()
	if paneCount < 1 {
		t.Fatal("paneCount must be positive")
	}

	srv, sess, cleanup := newCommandTestSession(t)
	sess.Clock = NewFakeClock(time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC))

	panes := make([]*mux.Pane, 0, paneCount)
	for i := 1; i <= paneCount; i++ {
		name := fmt.Sprintf("pane-%d", i)
		pane := newStandaloneProxyPane(uint32(i), name)
		pane.FeedOutput([]byte(strings.ToUpper(name) + "\r\n"))
		panes = append(panes, pane)
	}
	window := newTestWindowWithPanes(t, sess, 1, "main", panes...)
	setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, panes...)
	return srv, sess, cleanup
}

func seedMailboxMessage(t *testing.T, sess *Session, senderName string, recipientNames []string, req mailbox.SendRequest) mailbox.Message {
	t.Helper()

	var msg mailbox.Message
	var err error
	mustSessionMutation(t, sess, func(sess *Session) {
		req.Sender, err = mailboxAddressByName(sess, senderName)
		if err != nil {
			return
		}
		req.Recipients = make([]mailbox.PaneAddress, 0, len(recipientNames))
		for _, name := range recipientNames {
			var recipient mailbox.PaneAddress
			recipient, err = mailboxAddressByName(sess, name)
			if err != nil {
				return
			}
			req.Recipients = append(req.Recipients, recipient)
		}
		msg, err = sess.ensureMailbox().Send(req)
	})
	if err != nil {
		t.Fatalf("seed mailbox message: %v", err)
	}
	return msg
}

func mailboxAddressByName(sess *Session, name string) (mailbox.PaneAddress, error) {
	for _, pane := range sess.Panes {
		if pane != nil && pane.Meta.Name == name {
			return mailbox.PaneAddress{ID: pane.ID, Name: pane.Meta.Name, Host: pane.Meta.Host}, nil
		}
	}
	return mailbox.PaneAddress{}, fmt.Errorf("pane %q not found", name)
}

func decodeCaptureMap(t *testing.T, raw string) map[string]any {
	t.Helper()

	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("json.Unmarshal capture: %v\n%s", err, raw)
	}
	return out
}

func decodeCapturePaneMap(t *testing.T, raw string) map[string]any {
	t.Helper()

	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("json.Unmarshal pane capture: %v\n%s", err, raw)
	}
	return out
}

func capturePaneMap(t *testing.T, capture map[string]any, name string) map[string]any {
	t.Helper()

	panes, ok := capture["panes"].([]any)
	if !ok {
		t.Fatalf("capture panes = %#v, want array", capture["panes"])
	}
	for _, raw := range panes {
		pane, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("pane entry = %#v, want object", raw)
		}
		if pane["name"] == name {
			return pane
		}
	}
	t.Fatalf("pane %q not found in %#v", name, capture["panes"])
	return nil
}

func mailboxMap(t *testing.T, pane map[string]any) map[string]any {
	t.Helper()
	return jsonObject(t, pane, "mailbox")
}

func latestUnread(t *testing.T, mailbox map[string]any) []map[string]any {
	t.Helper()

	rawLatest, ok := mailbox["latest_unread"].([]any)
	if !ok {
		t.Fatalf("latest_unread = %#v, want array", mailbox["latest_unread"])
	}
	latest := make([]map[string]any, 0, len(rawLatest))
	for _, raw := range rawLatest {
		summary, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("latest unread entry = %#v, want object", raw)
		}
		latest = append(latest, summary)
	}
	return latest
}

func jsonObject(t *testing.T, parent map[string]any, key string) map[string]any {
	t.Helper()

	object, ok := parent[key].(map[string]any)
	if !ok {
		t.Fatalf("%s = %#v, want object", key, parent[key])
	}
	return object
}

func assertJSONNumber(t *testing.T, object map[string]any, key string, want int) {
	t.Helper()

	got, ok := object[key].(float64)
	if !ok {
		t.Fatalf("%s = %#v, want number", key, object[key])
	}
	if int(got) != want || got != float64(want) {
		t.Fatalf("%s = %v, want %d", key, got, want)
	}
}

func assertJSONString(t *testing.T, object map[string]any, key, want string) {
	t.Helper()

	got, ok := object[key].(string)
	if !ok {
		t.Fatalf("%s = %#v, want string", key, object[key])
	}
	if got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
	}
}

func assertJSONArrayLen(t *testing.T, object map[string]any, key string, want int) {
	t.Helper()

	got, ok := object[key].([]any)
	if !ok {
		t.Fatalf("%s = %#v, want array", key, object[key])
	}
	if len(got) != want {
		t.Fatalf("%s length = %d, want %d", key, len(got), want)
	}
}

func assertJSONMapLen(t *testing.T, object map[string]any, key string, want int) {
	t.Helper()

	got, ok := object[key].(map[string]any)
	if !ok {
		t.Fatalf("%s = %#v, want object", key, object[key])
	}
	if len(got) != want {
		t.Fatalf("%s length = %d, want %d", key, len(got), want)
	}
}

func assertTopicCount(t *testing.T, mailbox map[string]any, topic string, want int) {
	t.Helper()

	topics := jsonObject(t, mailbox, "topics")
	assertJSONNumber(t, topics, topic, want)
}

func assertStringSlice(t *testing.T, raw any, want []string) {
	t.Helper()

	gotRaw, ok := raw.([]any)
	if !ok {
		t.Fatalf("slice = %#v, want array", raw)
	}
	if len(gotRaw) != len(want) {
		t.Fatalf("slice length = %d, want %d (%#v)", len(gotRaw), len(want), raw)
	}
	for i, wantValue := range want {
		got, ok := gotRaw[i].(string)
		if !ok {
			t.Fatalf("slice[%d] = %#v, want string", i, gotRaw[i])
		}
		if got != wantValue {
			t.Fatalf("slice[%d] = %q, want %q", i, got, wantValue)
		}
	}
}
