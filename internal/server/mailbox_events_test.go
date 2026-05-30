package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/mailbox"
	"github.com/weill-labs/amux/internal/mux"
)

func TestMailboxDeliveredEventIsSummaryOnly(t *testing.T) {
	t.Parallel()

	_, sess, p1, p2, cleanup := newMailboxTestSession(t)
	defer cleanup()

	res := sess.enqueueEventSubscribe(sess.context(), eventFilter{
		Types:  []string{EventMessageDelivered},
		PaneID: p2.ID,
	}, false)
	defer sess.enqueueEventUnsubscribe(res.sub)

	msg := mustSendMailboxMessage(t, sess, p1, p2, "Review ready", "please review the full body")

	data := readMailboxEventData(t, res.sub, time.Second)
	if strings.Contains(string(data), "please review the full body") {
		t.Fatalf("delivered event leaked body: %s", string(data))
	}

	var ev Event
	mustUnmarshalJSON(t, data, &ev)
	if ev.Type != EventMessageDelivered {
		t.Fatalf("event type = %q, want %q", ev.Type, EventMessageDelivered)
	}
	if ev.PaneID != p2.ID || ev.PaneName != p2.Meta.Name {
		t.Fatalf("recipient fields = (%d, %q), want pane 2", ev.PaneID, ev.PaneName)
	}
	if ev.Message == nil {
		t.Fatal("event message summary is nil")
	}
	if ev.Message.ID != string(msg.ID) || ev.Message.Subject != "Review ready" {
		t.Fatalf("message summary = %+v, want sent message", ev.Message)
	}
	if ev.Message.From.ID != p1.ID || ev.Message.From.Name != p1.Meta.Name {
		t.Fatalf("message from = %+v, want pane 1", ev.Message.From)
	}
	if ev.Message.BodySize != len("please review the full body") || ev.Message.PartCount != 1 {
		t.Fatalf("message body summary = (%d, %d), want size and part count", ev.Message.BodySize, ev.Message.PartCount)
	}
}

func TestMailboxReadAndAckEvents(t *testing.T) {
	t.Parallel()

	_, sess, p1, p2, cleanup := newMailboxTestSession(t)
	defer cleanup()

	msg := mustSendMailboxMessage(t, sess, p1, p2, "Read me", "body")
	res := sess.enqueueEventSubscribe(sess.context(), eventFilter{
		Types:  []string{EventMessageRead, EventMessageAck},
		PaneID: p2.ID,
	}, false)
	defer sess.enqueueEventUnsubscribe(res.sub)

	if _, _, err := sess.enqueueMailboxRead(context.Background(), msg.ID, p2.ID, mailbox.ReadOptions{}); err != nil {
		t.Fatalf("enqueueMailboxRead: %v", err)
	}
	readEvent := readMailboxEvent(t, res.sub, time.Second)
	if readEvent.Type != EventMessageRead {
		t.Fatalf("read event type = %q, want %q", readEvent.Type, EventMessageRead)
	}
	if readEvent.Message == nil || readEvent.Message.ID != string(msg.ID) || readEvent.Message.ReadAt == "" {
		t.Fatalf("read event message = %+v, want read timestamp", readEvent.Message)
	}

	if _, err := sess.enqueueMailboxAck(context.Background(), msg.ID, p2.ID, mailbox.AckRequest{Status: "ok", Note: "done"}); err != nil {
		t.Fatalf("enqueueMailboxAck: %v", err)
	}
	ackEvent := readMailboxEvent(t, res.sub, time.Second)
	if ackEvent.Type != EventMessageAck {
		t.Fatalf("ack event type = %q, want %q", ackEvent.Type, EventMessageAck)
	}
	if ackEvent.Message == nil || ackEvent.Message.ID != string(msg.ID) || ackEvent.Message.AckStatus != "ok" || ackEvent.Message.AckedAt == "" {
		t.Fatalf("ack event message = %+v, want ack status and timestamp", ackEvent.Message)
	}
}

func TestMailboxEventsFilterByRecipientPane(t *testing.T) {
	t.Parallel()

	_, sess, p1, p2, cleanup := newMailboxTestSession(t)
	defer cleanup()
	p3 := newTestPane(sess, 3, "pane-3")
	mustSessionMutation(t, sess, func(sess *Session) {
		w := sess.activeWindow()
		if _, err := w.Split(mux.SplitHorizontal, p3); err != nil {
			t.Fatalf("Split: %v", err)
		}
		sess.Panes = w.Panes()
	})

	res := sess.enqueueEventSubscribe(sess.context(), eventFilter{
		Types:  []string{EventMessageDelivered},
		PaneID: p2.ID,
	}, false)
	defer sess.enqueueEventUnsubscribe(res.sub)

	mustSendMailboxMessage(t, sess, p1, p3, "Other pane", "body")
	mustSendMailboxMessage(t, sess, p1, p2, "Target pane", "body")

	ev := readMailboxEvent(t, res.sub, time.Second)
	if ev.PaneID != p2.ID || ev.Message == nil || ev.Message.Subject != "Target pane" {
		t.Fatalf("filtered event = %+v, want only pane-2 delivery", ev)
	}
	select {
	case data := <-res.sub.Ch:
		t.Fatalf("unexpected extra event: %s", string(data))
	case <-time.After(50 * time.Millisecond):
	}
}

func TestMailboxDeliveredReplayIncludesOnlyUnreadMessages(t *testing.T) {
	t.Parallel()

	_, sess, p1, p2, cleanup := newMailboxTestSession(t)
	defer cleanup()

	msg := mustSendMailboxMessage(t, sess, p1, p2, "Unread", "body")
	replay := sess.enqueueEventSubscribe(sess.context(), eventFilter{
		Types:  []string{EventMessageDelivered},
		PaneID: p2.ID,
	}, true)
	defer sess.enqueueEventUnsubscribe(replay.sub)

	if len(replay.initialState) != 1 {
		t.Fatalf("initial state length = %d, want 1", len(replay.initialState))
	}
	var ev Event
	mustUnmarshalJSON(t, replay.initialState[0], &ev)
	if ev.Type != EventMessageDelivered || ev.Message == nil || ev.Message.ID != string(msg.ID) {
		t.Fatalf("initial event = %+v, want unread delivery", ev)
	}

	if _, _, err := sess.enqueueMailboxRead(context.Background(), msg.ID, p2.ID, mailbox.ReadOptions{}); err != nil {
		t.Fatalf("enqueueMailboxRead: %v", err)
	}
	afterRead := sess.enqueueEventSubscribe(sess.context(), eventFilter{
		Types:  []string{EventMessageDelivered},
		PaneID: p2.ID,
	}, true)
	defer sess.enqueueEventUnsubscribe(afterRead.sub)
	if len(afterRead.initialState) != 0 {
		t.Fatalf("initial state after read length = %d, want 0", len(afterRead.initialState))
	}
}

func TestWaitMessageReturnsUnreadDelivery(t *testing.T) {
	t.Parallel()

	srv, sess, p1, p2, cleanup := newMailboxTestSession(t)
	defer cleanup()

	resultCh := make(chan struct {
		output string
		cmdErr string
	}, 1)
	go func() {
		resultCh <- runTestCommand(t, srv, sess, "wait", "msg", p2.Meta.Name, "--format", "json", "--timeout", "5s")
	}()
	waitForMailboxWaitSubscription(t, sess)

	msg := mustSendMailboxMessage(t, sess, p1, p2, "Incoming", "body hidden from wait")

	res := readWaitMessageResult(t, resultCh)
	if res.cmdErr != "" {
		t.Fatalf("wait msg error = %q", res.cmdErr)
	}
	if strings.Contains(res.output, "body hidden from wait") {
		t.Fatalf("wait msg leaked body: %s", res.output)
	}
	var summary struct {
		ID      string `json:"id"`
		Subject string `json:"subject"`
	}
	if err := json.Unmarshal([]byte(res.output), &summary); err != nil {
		t.Fatalf("unmarshal wait msg output %q: %v", res.output, err)
	}
	if summary.ID != string(msg.ID) || summary.Subject != "Incoming" {
		t.Fatalf("wait msg summary = %+v, want sent message", summary)
	}
}

func TestWaitMessageTimeout(t *testing.T) {
	t.Parallel()

	srv, sess, _, p2, cleanup := newMailboxTestSession(t)
	defer cleanup()

	res := runTestCommand(t, srv, sess, "wait", "msg", p2.Meta.Name, "--timeout", "1ms")
	if !strings.Contains(res.cmdErr, "timeout waiting for message for pane-2") {
		t.Fatalf("wait msg timeout error = %q", res.cmdErr)
	}
}

func TestWaitMessagePaneDisappearance(t *testing.T) {
	t.Parallel()

	srv, sess, _, p2, cleanup := newMailboxTestSession(t)
	defer cleanup()

	resultCh := make(chan struct {
		output string
		cmdErr string
	}, 1)
	go func() {
		resultCh <- runTestCommand(t, srv, sess, "wait", "msg", p2.Meta.Name, "--timeout", "5s")
	}()
	waitForMailboxWaitSubscription(t, sess)

	sess.enqueuePaneExit(p2.ID, "test")

	res := readWaitMessageResult(t, resultCh)
	if !strings.Contains(res.cmdErr, "pane \"pane-2\" disappeared while waiting for message") {
		t.Fatalf("wait msg pane disappearance error = %q", res.cmdErr)
	}
}

func newMailboxTestSession(t *testing.T) (*Server, *Session, *mux.Pane, *mux.Pane, func()) {
	t.Helper()

	srv, sess, cleanup := newCommandTestSession(t)
	p1 := newTestPane(sess, 1, "pane-1")
	p2 := newTestPane(sess, 2, "pane-2")
	w := newTestWindowWithPanes(t, sess, 1, "main", p1, p2)
	setSessionLayoutForTest(t, sess, w.ID, []*mux.Window{w}, p1, p2)
	return srv, sess, p1, p2, cleanup
}

func mustSendMailboxMessage(t *testing.T, sess *Session, sender, recipient *mux.Pane, subject, body string) mailbox.Message {
	t.Helper()

	msg, err := sess.enqueueMailboxSend(context.Background(), mailbox.SendRequest{
		Sender:     mailboxAddressForPane(sender),
		Recipients: []mailbox.PaneAddress{mailboxAddressForPane(recipient)},
		Subject:    subject,
		Body:       []byte(body),
	})
	if err != nil {
		t.Fatalf("enqueueMailboxSend: %v", err)
	}
	return msg
}

func mailboxAddressForPane(pane *mux.Pane) mailbox.PaneAddress {
	return mailbox.PaneAddress{
		ID:   pane.ID,
		Name: pane.Meta.Name,
		Host: pane.Meta.Host,
	}
}

func readMailboxEvent(t *testing.T, sub *eventSub, timeout time.Duration) Event {
	t.Helper()

	var ev Event
	mustUnmarshalJSON(t, readMailboxEventData(t, sub, timeout), &ev)
	return ev
}

func readMailboxEventData(t *testing.T, sub *eventSub, timeout time.Duration) []byte {
	t.Helper()

	select {
	case data := <-sub.Ch:
		return data
	case <-time.After(timeout):
		t.Fatal("timeout waiting for mailbox event")
		return nil
	}
}

func waitForMailboxWaitSubscription(t *testing.T, sess *Session) {
	t.Helper()
	waitUntil(t, func() bool {
		return mustSessionQuery(t, sess, func(sess *Session) int {
			count := 0
			for _, sub := range sess.eventSubs {
				if sub.Filter.PaneName == "pane-2" || sub.Filter.PaneID == 2 {
					count++
				}
			}
			return count
		}) > 0
	})
}

func readWaitMessageResult(t *testing.T, resultCh <-chan struct {
	output string
	cmdErr string
}) struct {
	output string
	cmdErr string
} {
	t.Helper()

	select {
	case res := <-resultCh:
		return res
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for wait msg result")
		return struct {
			output string
			cmdErr string
		}{}
	}
}
