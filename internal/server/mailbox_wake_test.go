package server

import (
	"testing"

	"github.com/weill-labs/amux/internal/mailbox"
)

func TestMailboxPendingCountsSkipsInvalidRecipients(t *testing.T) {
	t.Parallel()

	_, sess, cleanup := newCommandTestSession(t)
	defer cleanup()
	sess.ensureMailbox()

	counts := sess.mailboxPendingCounts([]mailbox.PaneAddress{
		{ID: 0, Name: "invalid"},
		{ID: 2, Name: "pane-2"},
	})
	if _, ok := counts[0]; ok {
		t.Fatalf("counts include invalid recipient: %#v", counts)
	}
	if got := counts[2]; got != 0 {
		t.Fatalf("counts[2] = %d, want empty mailbox count", got)
	}
}

func TestMaybeWakeMailboxRecipientSkipsMissingPane(t *testing.T) {
	t.Parallel()

	_, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	router := sess.ensureInputRouter()
	beforeQueues := len(router.paneQueues)
	sess.maybeWakeMailboxRecipient(99)
	if got := len(router.paneQueues); got != beforeQueues {
		t.Fatalf("input queue count = %d, want %d", got, beforeQueues)
	}
}
