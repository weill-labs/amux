package mailbox

import (
	"encoding/json"
	"fmt"
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
	if summary.LastEventSeq != 0 {
		t.Fatalf("LastEventSeq = %d, want 0 before event emission", summary.LastEventSeq)
	}

	updated, err := store.SetLastEventSeq(msg.ID, 2, 42)
	if err != nil {
		t.Fatalf("SetLastEventSeq: %v", err)
	}
	if updated.LastEventSeq != 42 {
		t.Fatalf("updated LastEventSeq = %d, want 42", updated.LastEventSeq)
	}

	delivery, err := store.DeliverySummary(msg.ID, 2)
	if err != nil {
		t.Fatalf("DeliverySummary: %v", err)
	}
	if delivery.LastEventSeq != 42 {
		t.Fatalf("DeliverySummary LastEventSeq = %d, want 42", delivery.LastEventSeq)
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
	unread, err := store.ListUnread(2)
	if err != nil {
		t.Fatalf("ListUnread after ack-before-read: %v", err)
	}
	if len(unread) != 0 {
		t.Fatalf("ListUnread after ack-before-read length = %d, want 0", len(unread))
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

func TestStoreDrainStatusCountsReadAndAckState(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	store := newTestStore(&now)

	empty, err := store.DrainStatus(2, DrainOptions{LatestLimit: 5})
	if err != nil {
		t.Fatalf("DrainStatus empty: %v", err)
	}
	if empty.Unread != 0 || empty.Unacked != 0 || empty.Pending != 0 || empty.PendingFingerprint != "" || len(empty.PendingIDs) != 0 || len(empty.Latest) != 0 {
		t.Fatalf("empty DrainStatus = %#v, want all zero fields", empty)
	}

	untouched := mustSend(t, store, SendRequest{
		Sender:     testAddress(1),
		Recipients: []PaneAddress{testAddress(2)},
		Subject:    "Untouched",
		Body:       []byte("untouched body"),
	})
	readOnly := mustSend(t, store, SendRequest{
		Sender:     testAddress(1),
		Recipients: []PaneAddress{testAddress(2)},
		Subject:    "Read only",
		Body:       []byte("read body"),
	})
	ackOnly := mustSend(t, store, SendRequest{
		Sender:     testAddress(1),
		Recipients: []PaneAddress{testAddress(2)},
		Subject:    "Ack only",
		Body:       []byte("ack body"),
	})
	done := mustSend(t, store, SendRequest{
		Sender:     testAddress(1),
		Recipients: []PaneAddress{testAddress(2)},
		Subject:    "Done",
		Body:       []byte("done body"),
	})

	now = now.Add(time.Minute)
	if _, _, err := store.Read(readOnly.ID, 2, ReadOptions{}); err != nil {
		t.Fatalf("Read(readOnly): %v", err)
	}
	if _, err := store.Ack(ackOnly.ID, 2, AckRequest{Status: "seen"}); err != nil {
		t.Fatalf("Ack(ackOnly): %v", err)
	}
	if _, _, err := store.Read(done.ID, 2, ReadOptions{}); err != nil {
		t.Fatalf("Read(done): %v", err)
	}
	if _, err := store.Ack(done.ID, 2, AckRequest{Status: "ok"}); err != nil {
		t.Fatalf("Ack(done): %v", err)
	}

	status, err := store.DrainStatus(2, DrainOptions{LatestLimit: 2})
	if err != nil {
		t.Fatalf("DrainStatus: %v", err)
	}
	if status.Unread != 2 || status.Unacked != 2 || status.Pending != 3 {
		t.Fatalf("DrainStatus counts = unread %d unacked %d pending %d, want 2/2/3", status.Unread, status.Unacked, status.Pending)
	}
	if status.PendingFingerprint == "" {
		t.Fatalf("PendingFingerprint is empty for pending status: %#v", status)
	}
	wantIDs := []MessageID{untouched.ID, readOnly.ID, ackOnly.ID}
	if fmt.Sprint(status.PendingIDs) != fmt.Sprint(wantIDs) {
		t.Fatalf("PendingIDs = %v, want %v", status.PendingIDs, wantIDs)
	}
	if len(status.Latest) != 2 || status.Latest[0].MessageID != ackOnly.ID || status.Latest[1].MessageID != readOnly.ID {
		t.Fatalf("Latest = %#v, want newest two pending summaries in reverse delivery order", status.Latest)
	}
}

func TestStoreDrainStatusFingerprintTracksReadAckProgress(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	store := newTestStore(&now)
	msg := mustSend(t, store, SendRequest{
		Sender:     testAddress(1),
		Recipients: []PaneAddress{testAddress(2)},
		Subject:    "Progress",
		Body:       []byte("body"),
	})

	untouched, err := store.DrainStatus(2, DrainOptions{})
	if err != nil {
		t.Fatalf("DrainStatus untouched: %v", err)
	}
	if untouched.PendingFingerprint == "" {
		t.Fatal("untouched fingerprint is empty")
	}

	now = now.Add(time.Minute)
	if _, _, err := store.Read(msg.ID, 2, ReadOptions{}); err != nil {
		t.Fatalf("Read: %v", err)
	}
	readOnly, err := store.DrainStatus(2, DrainOptions{})
	if err != nil {
		t.Fatalf("DrainStatus read-only: %v", err)
	}
	if readOnly.Pending != 1 || readOnly.Unread != 0 || readOnly.Unacked != 1 {
		t.Fatalf("read-only status = %#v, want one unacked pending delivery", readOnly)
	}
	if readOnly.PendingFingerprint == untouched.PendingFingerprint {
		t.Fatalf("fingerprint did not change after read: %q", readOnly.PendingFingerprint)
	}

	again, err := store.DrainStatus(2, DrainOptions{})
	if err != nil {
		t.Fatalf("DrainStatus again: %v", err)
	}
	if again.PendingFingerprint != readOnly.PendingFingerprint {
		t.Fatalf("stable read-only fingerprint = %q, want %q", again.PendingFingerprint, readOnly.PendingFingerprint)
	}

	now = now.Add(time.Minute)
	if _, err := store.Ack(msg.ID, 2, AckRequest{Status: "ok"}); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	done, err := store.DrainStatus(2, DrainOptions{})
	if err != nil {
		t.Fatalf("DrainStatus done: %v", err)
	}
	if done.Pending != 0 || done.PendingFingerprint != "" || len(done.PendingIDs) != 0 {
		t.Fatalf("done status = %#v, want empty pending state", done)
	}
}

func TestStoreDrainStatusRejectsInvalidRecipient(t *testing.T) {
	t.Parallel()

	var nilStore *Store
	if _, err := nilStore.DrainStatus(2, DrainOptions{}); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("nil DrainStatus error = %v, want nil store", err)
	}

	store := NewStore(Options{})
	if _, err := store.DrainStatus(0, DrainOptions{}); err == nil || !strings.Contains(err.Error(), "recipient pane ID") {
		t.Fatalf("zero-recipient DrainStatus error = %v, want recipient pane ID", err)
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
			name: "missing recipient name",
			mutate: func(req *SendRequest) {
				req.Recipients = []PaneAddress{{ID: 2}}
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
			name: "invalid subject utf8",
			mutate: func(req *SendRequest) {
				req.Subject = string([]byte{0xff})
			},
			wantReason: "UTF-8",
		},
		{
			name: "invalid sender name",
			mutate: func(req *SendRequest) {
				req.Sender = PaneAddress{ID: 1}
			},
			wantReason: "sender",
		},
		{
			name: "invalid topic",
			mutate: func(req *SendRequest) {
				req.Topics = []string{"bad topic"}
			},
			wantReason: "topic",
		},
		{
			name: "duplicate topic",
			mutate: func(req *SendRequest) {
				req.Topics = []string{"review", "review"}
			},
			wantReason: "duplicate",
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
			name: "missing metadata key",
			mutate: func(req *SendRequest) {
				req.Metadata = map[string]json.RawMessage{"": json.RawMessage(`"normal"`)}
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

func TestStoreRejectsLimitsWithoutStoring(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		limits     Limits
		setup      func(*Store)
		req        SendRequest
		wantReason string
	}{
		{
			name:       "subject byte limit",
			limits:     Limits{SubjectBytes: 4},
			req:        validSendRequest("too long", []byte("body")),
			wantReason: "subject",
		},
		{
			name:       "recipient count limit",
			limits:     Limits{RecipientsPerMsg: 1},
			req:        validSendRequest("Subject", []byte("body"), testAddress(2), testAddress(3)),
			wantReason: "recipient",
		},
		{
			name:       "one part limit",
			limits:     Limits{PartBytes: 3},
			req:        validSendRequest("Subject", []byte("body")),
			wantReason: "part",
		},
		{
			name:   "total body limit",
			limits: Limits{TotalBodyBytes: 7},
			req: SendRequest{
				Sender:     testAddress(1),
				Recipients: []PaneAddress{testAddress(2)},
				Subject:    "Subject",
				Parts: []MessagePart{
					{Bytes: []byte("1234")},
					{Bytes: []byte("5678")},
				},
			},
			wantReason: "body",
		},
		{
			name:   "metadata limit",
			limits: Limits{MetadataBytes: 8},
			req: SendRequest{
				Sender:     testAddress(1),
				Recipients: []PaneAddress{testAddress(2)},
				Subject:    "Subject",
				Body:       []byte("body"),
				Metadata:   map[string]json.RawMessage{"priority": json.RawMessage(`"normal"`)},
			},
			wantReason: "metadata",
		},
		{
			name:       "stored message limit",
			limits:     Limits{StoredMessages: 1},
			setup:      seedOneMessage,
			req:        validSendRequest("Subject", []byte("body")),
			wantReason: "stored message",
		},
		{
			name:       "mailbox byte limit",
			limits:     Limits{StoredMailboxBytes: 3},
			req:        validSendRequest("Subject", []byte("body")),
			wantReason: "byte limit",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
			store := NewStore(Options{
				Now:    func() time.Time { return now },
				Limits: tt.limits,
			})
			if tt.setup != nil {
				tt.setup(store)
			}
			before := store.Len()

			if _, err := store.Send(tt.req); err == nil || !strings.Contains(err.Error(), tt.wantReason) {
				t.Fatalf("Send error = %v, want reason containing %q", err, tt.wantReason)
			}
			if store.Len() != before {
				t.Fatalf("Len after rejected Send = %d, want %d", store.Len(), before)
			}
		})
	}
}

func TestStoreRejectsInvalidPartsWithoutStoring(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		req        SendRequest
		wantReason string
	}{
		{
			name: "body and parts",
			req: SendRequest{
				Sender:     testAddress(1),
				Recipients: []PaneAddress{testAddress(2)},
				Subject:    "Subject",
				Body:       []byte("body"),
				Parts:      []MessagePart{{Bytes: []byte("part")}},
			},
			wantReason: "mutually exclusive",
		},
		{
			name: "empty part",
			req: SendRequest{
				Sender:     testAddress(1),
				Recipients: []PaneAddress{testAddress(2)},
				Subject:    "Subject",
				Parts:      []MessagePart{{Name: "empty"}},
			},
			wantReason: "empty",
		},
		{
			name: "invalid utf8",
			req: SendRequest{
				Sender:     testAddress(1),
				Recipients: []PaneAddress{testAddress(2)},
				Subject:    "Subject",
				Parts:      []MessagePart{{Name: "body", Bytes: []byte{0xff}}},
			},
			wantReason: "UTF-8",
		},
		{
			name: "control byte",
			req: SendRequest{
				Sender:     testAddress(1),
				Recipients: []PaneAddress{testAddress(2)},
				Subject:    "Subject",
				Parts:      []MessagePart{{Name: "body", Bytes: []byte{'a', 0x1b}}},
			},
			wantReason: "control byte",
		},
		{
			name: "unsupported encoding",
			req: SendRequest{
				Sender:     testAddress(1),
				Recipients: []PaneAddress{testAddress(2)},
				Subject:    "Subject",
				Parts:      []MessagePart{{Name: "body", Encoding: "gzip", Bytes: []byte("body")}},
			},
			wantReason: "encoding",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
			store := newTestStore(&now)

			if _, err := store.Send(tt.req); err == nil || !strings.Contains(err.Error(), tt.wantReason) {
				t.Fatalf("Send error = %v, want reason containing %q", err, tt.wantReason)
			}
			if store.Len() != 0 {
				t.Fatalf("Len after rejected Send = %d, want 0", store.Len())
			}
		})
	}
}

func TestStoreAcceptsBase64Part(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	store := newTestStore(&now)

	msg := mustSend(t, store, SendRequest{
		Sender:     testAddress(1),
		Recipients: []PaneAddress{testAddress(2)},
		Subject:    "Binary",
		Parts: []MessagePart{{
			Name:     "payload",
			Encoding: EncodingBase64,
			Bytes:    []byte("AAEC"),
		}},
	})

	if msg.Parts[0].Encoding != EncodingBase64 || msg.Parts[0].Size != len("AAEC") {
		t.Fatalf("base64 part = %#v, want preserved encoding and size", msg.Parts[0])
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
	if _, _, err := store.Read(msg.ID, 0, ReadOptions{}); err == nil || !strings.Contains(err.Error(), "recipient") {
		t.Fatalf("Read invalid recipient error = %v, want recipient error", err)
	}
	if _, _, err := store.Read(msg.ID, 3, ReadOptions{}); err == nil || !strings.Contains(err.Error(), "not delivered") {
		t.Fatalf("Read non-recipient error = %v, want not delivered", err)
	}
	if _, err := store.Ack("msg-999999", 2, AckRequest{Status: "ok"}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("Ack unknown error = %v, want not found", err)
	}
	if _, err := store.Ack(msg.ID, 0, AckRequest{Status: "ok"}); err == nil || !strings.Contains(err.Error(), "recipient") {
		t.Fatalf("Ack invalid recipient error = %v, want recipient error", err)
	}
	if _, err := store.Ack(msg.ID, 3, AckRequest{Status: "ok"}); err == nil || !strings.Contains(err.Error(), "not delivered") {
		t.Fatalf("Ack non-recipient error = %v, want not delivered", err)
	}
	if _, err := store.Ack(msg.ID, 2, AckRequest{Status: "ok", Note: strings.Repeat("x", DefaultLimits().AckNoteBytes+1)}); err == nil || !strings.Contains(err.Error(), "ack note") {
		t.Fatalf("Ack oversized note error = %v, want ack note error", err)
	}
	if _, err := store.ListUnread(0); err == nil || !strings.Contains(err.Error(), "recipient") {
		t.Fatalf("ListUnread invalid pane error = %v, want recipient error", err)
	}
	if _, err := store.DeliverySummary("msg-999999", 2); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("DeliverySummary unknown error = %v, want not found", err)
	}
	if _, err := store.SetLastEventSeq(msg.ID, 3, 99); err == nil || !strings.Contains(err.Error(), "not delivered") {
		t.Fatalf("SetLastEventSeq non-recipient error = %v, want not delivered", err)
	}
}

func TestStoreNilReceiverErrors(t *testing.T) {
	t.Parallel()

	var store *Store
	if store.Len() != 0 {
		t.Fatalf("nil Len = %d, want 0", store.Len())
	}
	if snapshot := store.Snapshot(); snapshot.NextSeq != 0 || len(snapshot.Messages) != 0 || len(snapshot.Deliveries) != 0 {
		t.Fatalf("nil Snapshot = %#v, want empty", snapshot)
	}
	if got := store.MaxLastEventSeq(); got != 0 {
		t.Fatalf("nil MaxLastEventSeq = %d, want 0", got)
	}
	if _, ok := store.Message("msg-000001"); ok {
		t.Fatalf("nil Message returned ok")
	}
	if _, err := store.Send(validSendRequest("Subject", []byte("body"))); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("nil Send error = %v, want nil store error", err)
	}
	if _, err := store.ListUnread(2); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("nil ListUnread error = %v, want nil store error", err)
	}
	if _, err := store.DeliverySummary("msg-000001", 2); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("nil DeliverySummary error = %v, want nil store error", err)
	}
	if _, err := store.SetLastEventSeq("msg-000001", 2, 1); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("nil SetLastEventSeq error = %v, want nil store error", err)
	}
	if _, _, err := store.Read("msg-000001", 2, ReadOptions{}); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("nil Read error = %v, want nil store error", err)
	}
	if _, err := store.Ack("msg-000001", 2, AckRequest{Status: "ok"}); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("nil Ack error = %v, want nil store error", err)
	}
}

func TestStoreNewStoreUsesRealClockWhenNoClockInjected(t *testing.T) {
	t.Parallel()

	store := NewStore(Options{})
	before := time.Now().UTC()
	msg := mustSend(t, store, validSendRequest("Subject", []byte("body")))
	after := time.Now().UTC()

	if msg.CreatedAt.Before(before) || msg.CreatedAt.After(after) {
		t.Fatalf("CreatedAt = %s, want between %s and %s", msg.CreatedAt, before, after)
	}
}

func TestStoreMessageAndEmptyUnreadMisses(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	store := newTestStore(&now)

	if _, ok := store.Message("msg-000001"); ok {
		t.Fatalf("Message for missing ID returned ok")
	}
	unread, err := store.ListUnread(99)
	if err != nil {
		t.Fatalf("ListUnread for empty valid pane: %v", err)
	}
	if len(unread) != 0 {
		t.Fatalf("empty ListUnread length = %d, want 0", len(unread))
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

func TestStoreSnapshotRestorePreservesMessagesDeliveriesAndNextID(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	store := newTestStore(&now)

	root := mustSend(t, store, SendRequest{
		Sender:     testAddress(1),
		Recipients: []PaneAddress{testAddress(2)},
		Topics:     []string{"review"},
		Groups:     []string{"agents"},
		Subject:    "Root",
		Body:       []byte("root body"),
		Metadata: map[string]json.RawMessage{
			"priority": json.RawMessage(`"high"`),
		},
	})
	if _, err := store.SetLastEventSeq(root.ID, 2, 7); err != nil {
		t.Fatalf("SetLastEventSeq root: %v", err)
	}

	now = now.Add(time.Minute)
	_, readState, err := store.Read(root.ID, 2, ReadOptions{})
	if err != nil {
		t.Fatalf("Read root: %v", err)
	}

	now = now.Add(time.Minute)
	reply := mustSend(t, store, SendRequest{
		Sender:     testAddress(2),
		Recipients: []PaneAddress{testAddress(1)},
		Subject:    "Reply",
		Body:       []byte("reply body"),
		ReplyTo:    root.ID,
	})
	if _, err := store.SetLastEventSeq(reply.ID, 1, 8); err != nil {
		t.Fatalf("SetLastEventSeq reply: %v", err)
	}

	now = now.Add(time.Minute)
	ackState, err := store.Ack(reply.ID, 1, AckRequest{Status: "seen", Note: "queued"})
	if err != nil {
		t.Fatalf("Ack reply: %v", err)
	}

	restored, err := RestoreSnapshot(store.Snapshot(), Options{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("RestoreSnapshot: %v", err)
	}

	restoredRoot, ok := restored.Message(root.ID)
	if !ok {
		t.Fatalf("restored root %q not found", root.ID)
	}
	if restoredRoot.ThreadID != root.ThreadID || len(restoredRoot.Replies) != 1 || restoredRoot.Replies[0] != reply.ID {
		t.Fatalf("restored root thread fields = (%q, %#v), want reply %q", restoredRoot.ThreadID, restoredRoot.Replies, reply.ID)
	}
	if got := string(restoredRoot.Metadata["priority"]); got != `"high"` {
		t.Fatalf("restored metadata priority = %s, want high", got)
	}

	restoredRootDelivery, err := restored.DeliverySummary(root.ID, 2)
	if err != nil {
		t.Fatalf("restored root DeliverySummary: %v", err)
	}
	if restoredRootDelivery.ReadAt != readState.ReadAt || restoredRootDelivery.LastEventSeq != 7 {
		t.Fatalf("restored root delivery = %#v, want read_at %s and seq 7", restoredRootDelivery, readState.ReadAt)
	}

	restoredReply, ok := restored.Message(reply.ID)
	if !ok {
		t.Fatalf("restored reply %q not found", reply.ID)
	}
	if restoredReply.ThreadID != root.ThreadID || restoredReply.InReplyTo != root.ID {
		t.Fatalf("restored reply thread fields = (%q, %q), want (%q, %q)", restoredReply.ThreadID, restoredReply.InReplyTo, root.ThreadID, root.ID)
	}
	restoredReplyDelivery, err := restored.DeliverySummary(reply.ID, 1)
	if err != nil {
		t.Fatalf("restored reply DeliverySummary: %v", err)
	}
	if restoredReplyDelivery.AckedAt != ackState.AckedAt || restoredReplyDelivery.AckStatus != "seen" || restoredReplyDelivery.AckNote != "queued" || restoredReplyDelivery.LastEventSeq != 8 {
		t.Fatalf("restored reply delivery = %#v, want ack state and seq 8", restoredReplyDelivery)
	}
	if got := restored.MaxLastEventSeq(); got != 8 {
		t.Fatalf("MaxLastEventSeq = %d, want 8", got)
	}

	next := mustSend(t, restored, validSendRequest("Next", []byte("next body")))
	assertMessageID(t, next.ID, "msg-000003")
}

func TestStoreRestoreSnapshotRejectsMalformedState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		snapshot Snapshot
		want     string
	}{
		{
			name:     "missing message id",
			snapshot: Snapshot{Messages: []Message{{Subject: "missing id"}}},
			want:     "message ID",
		},
		{
			name: "duplicate message id",
			snapshot: Snapshot{Messages: []Message{
				snapshotMessage("msg-000001"),
				snapshotMessage("msg-000001"),
			}},
			want: "duplicate",
		},
		{
			name: "missing delivery message id",
			snapshot: Snapshot{
				Messages:   []Message{snapshotMessage("msg-000001")},
				Deliveries: []DeliveryState{{Recipient: testAddress(2)}},
			},
			want: "delivery message ID",
		},
		{
			name: "missing delivery recipient id",
			snapshot: Snapshot{
				Messages:   []Message{snapshotMessage("msg-000001")},
				Deliveries: []DeliveryState{{MessageID: "msg-000001"}},
			},
			want: "recipient pane ID",
		},
		{
			name: "delivery references missing message",
			snapshot: Snapshot{
				Messages:   []Message{snapshotMessage("msg-000001")},
				Deliveries: []DeliveryState{{MessageID: "msg-000002", Recipient: testAddress(2)}},
			},
			want: "missing message",
		},
		{
			name: "duplicate delivery",
			snapshot: Snapshot{
				Messages: []Message{snapshotMessage("msg-000001")},
				Deliveries: []DeliveryState{
					{MessageID: "msg-000001", Recipient: testAddress(2)},
					{MessageID: "msg-000001", Recipient: testAddress(2)},
				},
			},
			want: "duplicate",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := RestoreSnapshot(tt.snapshot, Options{}); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("RestoreSnapshot error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestStoreRestoreSnapshotUsesNextSeqForNonStandardMessageIDs(t *testing.T) {
	t.Parallel()

	restored, err := RestoreSnapshot(Snapshot{
		NextSeq: 5,
		Messages: []Message{{
			ID:       "external-message",
			ThreadID: "external-thread",
			Sender:   testAddress(1),
			Parts:    []MessagePart{{Name: "body", ContentType: DefaultContentType, Encoding: EncodingUTF8, Bytes: []byte("body"), Size: 4}},
		}},
		Deliveries: []DeliveryState{{MessageID: "external-message", Recipient: testAddress(2)}},
	}, Options{})
	if err != nil {
		t.Fatalf("RestoreSnapshot: %v", err)
	}

	next := mustSend(t, restored, validSendRequest("Next", []byte("body")))
	assertMessageID(t, next.ID, "msg-000005")
}

func TestStoreListUnreadSortsByMessageID(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	store := newTestStore(&now)

	first := mustSend(t, store, validSendRequest("First", []byte("one")))
	second := mustSend(t, store, validSendRequest("Second", []byte("two")))

	unread, err := store.ListUnread(2)
	if err != nil {
		t.Fatalf("ListUnread: %v", err)
	}
	if len(unread) != 2 {
		t.Fatalf("ListUnread length = %d, want 2", len(unread))
	}
	if unread[0].MessageID != first.ID || unread[1].MessageID != second.ID {
		t.Fatalf("ListUnread IDs = [%q, %q], want message ID order", unread[0].MessageID, unread[1].MessageID)
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

	if _, err := store.Send(SendRequest{
		Sender:     testAddress(2),
		Recipients: []PaneAddress{testAddress(1)},
		Subject:    "Bad reply",
		Body:       []byte("reply"),
		ReplyTo:    root.ID,
		ThreadID:   "other-thread",
	}); err == nil || !strings.Contains(err.Error(), "thread") {
		t.Fatalf("Send reply to mismatched thread error = %v, want thread error", err)
	}
}

func TestStoreExplicitThread(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	store := newTestStore(&now)
	msg := mustSend(t, store, SendRequest{
		Sender:     testAddress(1),
		Recipients: []PaneAddress{testAddress(2)},
		Subject:    "Explicit",
		Body:       []byte("body"),
		ThreadID:   "thread-review",
	})

	if msg.ThreadID != "thread-review" {
		t.Fatalf("ThreadID = %q, want explicit thread", msg.ThreadID)
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
	return PaneAddress{ID: id, Name: fmt.Sprintf("pane-%d", id), Host: "local"}
}

func validSendRequest(subject string, body []byte, recipients ...PaneAddress) SendRequest {
	if len(recipients) == 0 {
		recipients = []PaneAddress{testAddress(2)}
	}
	return SendRequest{
		Sender:     testAddress(1),
		Recipients: recipients,
		Subject:    subject,
		Body:       body,
	}
}

func snapshotMessage(id MessageID) Message {
	return Message{
		ID:       id,
		ThreadID: ThreadID(id),
		Sender:   testAddress(1),
		Parts: []MessagePart{{
			Name:        "body",
			ContentType: DefaultContentType,
			Encoding:    EncodingUTF8,
			Bytes:       []byte("body"),
			Size:        len("body"),
		}},
	}
}

func seedOneMessage(store *Store) {
	_, err := store.Send(validSendRequest("Seed", []byte("seed")))
	if err != nil {
		panic(err)
	}
}
