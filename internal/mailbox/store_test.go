package mailbox

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestStoreSendCreatesMessageAndUnreadDelivery(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	store := newTestStore(&now)

	msg, err := store.Send(SendRequest{
		Sender:     testAddress(1),
		Recipients: []PaneAddress{testAddress(2)},
		Topics:     []string{"review"},
		Groups:     []string{"backend"},
		Subject:    "Review ready",
		Body:       []byte("please review"),
		Metadata: map[string]json.RawMessage{
			"priority": json.RawMessage(`"normal"`),
		},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	assertMessageID(t, msg.ID, "msg-000001")
	if msg.ThreadID != ThreadID(msg.ID) {
		t.Fatalf("ThreadID = %q, want message ID", msg.ThreadID)
	}
	if msg.CreatedAt != now || msg.UpdatedAt != now {
		t.Fatalf("message timestamps = (%s, %s), want %s", msg.CreatedAt, msg.UpdatedAt, now)
	}
	if msg.Sender != testAddress(1) {
		t.Fatalf("Sender = %#v, want pane 1", msg.Sender)
	}
	if len(msg.Recipients) != 1 || msg.Recipients[0] != testAddress(2) {
		t.Fatalf("Recipients = %#v, want pane 2", msg.Recipients)
	}
	if len(msg.Parts) != 1 {
		t.Fatalf("Parts length = %d, want 1", len(msg.Parts))
	}
	if string(msg.Parts[0].Bytes) != "please review" {
		t.Fatalf("Part bytes = %q, want body", string(msg.Parts[0].Bytes))
	}
	if msg.Parts[0].ContentType != DefaultContentType || msg.Parts[0].Encoding != EncodingUTF8 || msg.Parts[0].Size != len("please review") {
		t.Fatalf("Part metadata = %#v, want default text/plain utf-8 part", msg.Parts[0])
	}

	unread, err := store.ListUnread(2)
	if err != nil {
		t.Fatalf("ListUnread: %v", err)
	}
	if len(unread) != 1 {
		t.Fatalf("ListUnread length = %d, want 1", len(unread))
	}
	summary := unread[0]
	if summary.MessageID != msg.ID || summary.Subject != "Review ready" {
		t.Fatalf("summary = %#v, want created message", summary)
	}
	if summary.BodySize != len("please review") || summary.PartCount != 1 {
		t.Fatalf("summary body fields = (%d, %d), want body size and part count", summary.BodySize, summary.PartCount)
	}
	if summary.DeliveredAt != now || !summary.ReadAt.IsZero() || !summary.AckedAt.IsZero() {
		t.Fatalf("delivery timestamps = %#v, want delivered only", summary)
	}
}

func TestStoreListUnreadReadAndAckTransitions(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	store := newTestStore(&now)
	msg := mustSend(t, store, SendRequest{
		Sender:     testAddress(1),
		Recipients: []PaneAddress{testAddress(2)},
		Subject:    "Read me",
		Body:       []byte("body"),
	})

	now = now.Add(time.Minute)
	readMsg, readState, err := store.Read(msg.ID, 2, ReadOptions{})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if readMsg.ID != msg.ID || string(readMsg.Parts[0].Bytes) != "body" {
		t.Fatalf("Read returned %#v, want original message body", readMsg)
	}
	if readState.ReadAt != now {
		t.Fatalf("ReadAt = %s, want %s", readState.ReadAt, now)
	}

	unread, err := store.ListUnread(2)
	if err != nil {
		t.Fatalf("ListUnread after read: %v", err)
	}
	if len(unread) != 0 {
		t.Fatalf("ListUnread after read length = %d, want 0", len(unread))
	}

	now = now.Add(time.Minute)
	acked, err := store.Ack(msg.ID, 2, AckRequest{Status: "ok", Note: "done"})
	if err != nil {
		t.Fatalf("Ack: %v", err)
	}
	if acked.AckedAt != now || acked.AckStatus != "ok" || acked.AckNote != "done" {
		t.Fatalf("ack state = %#v, want explicit ack", acked)
	}
}

func TestStoreReadPeekDoesNotMarkRead(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	store := newTestStore(&now)
	msg := mustSend(t, store, SendRequest{
		Sender:     testAddress(1),
		Recipients: []PaneAddress{testAddress(2)},
		Subject:    "Peek",
		Body:       []byte("body"),
	})

	now = now.Add(time.Minute)
	_, state, err := store.Read(msg.ID, 2, ReadOptions{Peek: true})
	if err != nil {
		t.Fatalf("Read peek: %v", err)
	}
	if !state.ReadAt.IsZero() {
		t.Fatalf("ReadAt after peek = %s, want zero", state.ReadAt)
	}

	unread, err := store.ListUnread(2)
	if err != nil {
		t.Fatalf("ListUnread after peek: %v", err)
	}
	if len(unread) != 1 {
		t.Fatalf("ListUnread after peek length = %d, want 1", len(unread))
	}
}

func TestStoreAckBeforeReadAndIdempotentRepeat(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	store := newTestStore(&now)
	msg := mustSend(t, store, SendRequest{
		Sender:     testAddress(1),
		Recipients: []PaneAddress{testAddress(2)},
		Subject:    "Ack first",
		Body:       []byte("body"),
	})

	now = now.Add(time.Minute)
	first, err := store.Ack(msg.ID, 2, AckRequest{Status: "seen", Note: "queued"})
	if err != nil {
		t.Fatalf("Ack before read: %v", err)
	}
	if !first.ReadAt.IsZero() {
		t.Fatalf("ReadAt after ack-before-read = %s, want zero", first.ReadAt)
	}

	now = now.Add(time.Minute)
	repeated, err := store.Ack(msg.ID, 2, AckRequest{Status: "seen", Note: "queued"})
	if err != nil {
		t.Fatalf("repeat Ack: %v", err)
	}
	if repeated.AckedAt != first.AckedAt {
		t.Fatalf("repeat AckedAt = %s, want original %s", repeated.AckedAt, first.AckedAt)
	}
}

func TestStoreMultiRecipientDeliveryTracksPerRecipientState(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	store := newTestStore(&now)
	msg := mustSend(t, store, SendRequest{
		Sender:     testAddress(1),
		Recipients: []PaneAddress{testAddress(2), testAddress(3)},
		Subject:    "Fanout",
		Body:       []byte("same body"),
	})

	now = now.Add(time.Minute)
	if _, _, err := store.Read(msg.ID, 2, ReadOptions{}); err != nil {
		t.Fatalf("Read recipient 2: %v", err)
	}
	if _, err := store.Ack(msg.ID, 2, AckRequest{Status: "ok"}); err != nil {
		t.Fatalf("Ack recipient 2: %v", err)
	}

	unread2, err := store.ListUnread(2)
	if err != nil {
		t.Fatalf("ListUnread recipient 2: %v", err)
	}
	if len(unread2) != 0 {
		t.Fatalf("recipient 2 unread length = %d, want 0", len(unread2))
	}
	unread3, err := store.ListUnread(3)
	if err != nil {
		t.Fatalf("ListUnread recipient 3: %v", err)
	}
	if len(unread3) != 1 || unread3[0].MessageID != msg.ID {
		t.Fatalf("recipient 3 unread = %#v, want original message", unread3)
	}
	if !unread3[0].ReadAt.IsZero() || !unread3[0].AckedAt.IsZero() {
		t.Fatalf("recipient 3 state = %#v, want untouched delivery", unread3[0])
	}
}

func TestStoreRejectsInvalidSendWithoutStoring(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		mutate     func(*SendRequest)
		wantReason string
	}{
		{
			name: "missing sender",
			mutate: func(req *SendRequest) {
				req.Sender = PaneAddress{}
			},
			wantReason: "sender",
		},
		{
			name: "missing recipients",
			mutate: func(req *SendRequest) {
				req.Recipients = nil
			},
			wantReason: "recipient",
		},
		{
			name: "invalid recipient",
			mutate: func(req *SendRequest) {
				req.Recipients = []PaneAddress{{ID: 0, Name: "pane-0"}}
			},
			wantReason: "recipient",
		},
		{
			name: "duplicate recipient",
			mutate: func(req *SendRequest) {
				req.Recipients = []PaneAddress{testAddress(2), testAddress(2)}
			},
			wantReason: "duplicate",
		},
		{
			name: "empty body",
			mutate: func(req *SendRequest) {
				req.Body = nil
			},
			wantReason: "body",
		},
		{
			name: "invalid subject",
			mutate: func(req *SendRequest) {
				req.Subject = "two\nlines"
			},
			wantReason: "subject",
		},
		{
			name: "invalid topic",
			mutate: func(req *SendRequest) {
				req.Topics = []string{"bad topic"}
			},
			wantReason: "topic",
		},
		{
			name: "invalid group",
			mutate: func(req *SendRequest) {
				req.Groups = []string{"-bad"}
			},
			wantReason: "group",
		},
		{
			name: "invalid metadata json",
			mutate: func(req *SendRequest) {
				req.Metadata = map[string]json.RawMessage{"priority": json.RawMessage(`{`)}
			},
			wantReason: "metadata",
		},
		{
			name: "reserved metadata key",
			mutate: func(req *SendRequest) {
				req.Metadata = map[string]json.RawMessage{"amux.priority": json.RawMessage(`"normal"`)}
			},
			wantReason: "reserved",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
			store := newTestStore(&now)
			req := SendRequest{
				Sender:     testAddress(1),
				Recipients: []PaneAddress{testAddress(2)},
				Subject:    "Subject",
				Body:       []byte("body"),
			}
			tt.mutate(&req)

			if _, err := store.Send(req); err == nil || !strings.Contains(err.Error(), tt.wantReason) {
				t.Fatalf("Send error = %v, want reason containing %q", err, tt.wantReason)
			}
			if store.Len() != 0 {
				t.Fatalf("Len after rejected Send = %d, want 0", store.Len())
			}

			msg := mustSend(t, store, SendRequest{
				Sender:     testAddress(1),
				Recipients: []PaneAddress{testAddress(2)},
				Subject:    "Next",
				Body:       []byte("body"),
			})
			assertMessageID(t, msg.ID, "msg-000001")
		})
	}
}

func TestStoreRejectsReadAndAckNoOps(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	store := newTestStore(&now)
	msg := mustSend(t, store, SendRequest{
		Sender:     testAddress(1),
		Recipients: []PaneAddress{testAddress(2)},
		Subject:    "No ops",
		Body:       []byte("body"),
	})

	if _, _, err := store.Read("msg-999999", 2, ReadOptions{}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("Read unknown error = %v, want not found", err)
	}
	if _, _, err := store.Read(msg.ID, 3, ReadOptions{}); err == nil || !strings.Contains(err.Error(), "not delivered") {
		t.Fatalf("Read non-recipient error = %v, want not delivered", err)
	}
	if _, err := store.Ack("msg-999999", 2, AckRequest{Status: "ok"}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("Ack unknown error = %v, want not found", err)
	}
	if _, err := store.Ack(msg.ID, 3, AckRequest{Status: "ok"}); err == nil || !strings.Contains(err.Error(), "not delivered") {
		t.Fatalf("Ack non-recipient error = %v, want not delivered", err)
	}
	if _, err := store.ListUnread(0); err == nil || !strings.Contains(err.Error(), "recipient") {
		t.Fatalf("ListUnread invalid pane error = %v, want recipient error", err)
	}
}

func TestStoreAllocatesUniqueStableIDs(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	store := newTestStore(&now)

	for _, wantID := range []MessageID{"msg-000001", "msg-000002", "msg-000003"} {
		msg := mustSend(t, store, SendRequest{
			Sender:     testAddress(1),
			Recipients: []PaneAddress{testAddress(2)},
			Subject:    "Subject",
			Body:       []byte("body"),
		})
		assertMessageID(t, msg.ID, wantID)
	}
}

func TestStoreDefensivelyCopiesBodiesAndMetadata(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	store := newTestStore(&now)
	body := []byte("immutable")
	metadataValue := json.RawMessage(`"normal"`)
	msg := mustSend(t, store, SendRequest{
		Sender:     testAddress(1),
		Recipients: []PaneAddress{testAddress(2)},
		Subject:    "Copies",
		Body:       body,
		Metadata:   map[string]json.RawMessage{"priority": metadataValue},
	})
	body[0] = 'X'
	metadataValue[1] = 'X'

	readMsg, _, err := store.Read(msg.ID, 2, ReadOptions{Peek: true})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	readMsg.Parts[0].Bytes[0] = 'Y'
	readMsg.Metadata["priority"][1] = 'Y'

	again, _, err := store.Read(msg.ID, 2, ReadOptions{Peek: true})
	if err != nil {
		t.Fatalf("second Read: %v", err)
	}
	if string(again.Parts[0].Bytes) != "immutable" {
		t.Fatalf("stored body = %q, want original", string(again.Parts[0].Bytes))
	}
	if string(again.Metadata["priority"]) != `"normal"` {
		t.Fatalf("stored metadata = %s, want original", string(again.Metadata["priority"]))
	}

	stored, ok := store.Message(msg.ID)
	if !ok {
		t.Fatalf("Message(%q) not found", msg.ID)
	}
	stored.Parts[0].Bytes[0] = 'Z'
	fresh, ok := store.Message(msg.ID)
	if !ok {
		t.Fatalf("Message(%q) second lookup not found", msg.ID)
	}
	if string(fresh.Parts[0].Bytes) != "immutable" {
		t.Fatalf("Message returned mutable body; got %q", string(fresh.Parts[0].Bytes))
	}
}

func TestStoreRepliesLinkThreads(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	store := newTestStore(&now)
	root := mustSend(t, store, SendRequest{
		Sender:     testAddress(1),
		Recipients: []PaneAddress{testAddress(2)},
		Subject:    "Root",
		Body:       []byte("root"),
	})

	reply := mustSend(t, store, SendRequest{
		Sender:     testAddress(2),
		Recipients: []PaneAddress{testAddress(1)},
		Subject:    "Reply",
		Body:       []byte("reply"),
		ReplyTo:    root.ID,
	})

	if reply.ThreadID != ThreadID(root.ID) || reply.InReplyTo != root.ID {
		t.Fatalf("reply links = (%q, %q), want root thread and parent", reply.ThreadID, reply.InReplyTo)
	}
	storedRoot, ok := store.Message(root.ID)
	if !ok {
		t.Fatalf("Message(%q) not found", root.ID)
	}
	if len(storedRoot.Replies) != 1 || storedRoot.Replies[0] != reply.ID {
		t.Fatalf("root Replies = %#v, want reply ID", storedRoot.Replies)
	}

	if _, err := store.Send(SendRequest{
		Sender:     testAddress(1),
		Recipients: []PaneAddress{testAddress(2)},
		Subject:    "Missing reply",
		Body:       []byte("body"),
		ReplyTo:    "msg-999999",
	}); err == nil || !strings.Contains(err.Error(), "reply") {
		t.Fatalf("Send reply to missing parent error = %v, want reply error", err)
	}
}

func newTestStore(now *time.Time) *Store {
	return NewStore(Options{
		Now: func() time.Time {
			return now.UTC()
		},
	})
}

func mustSend(t *testing.T, store *Store, req SendRequest) Message {
	t.Helper()
	msg, err := store.Send(req)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	return msg
}

func assertMessageID(t *testing.T, got, want MessageID) {
	t.Helper()
	if got != want {
		t.Fatalf("MessageID = %q, want %q", got, want)
	}
}

func testAddress(id uint32) PaneAddress {
	return PaneAddress{ID: id, Name: "pane-" + string(rune('0'+id)), Host: "local"}
}
