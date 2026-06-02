package server

import (
	"testing"

	"github.com/weill-labs/amux/internal/mailbox"
)

func TestMailboxPaneWakeEnabledRequiresExplicitOptIn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		kv   map[string]string
		want bool
	}{
		{name: "empty metadata", kv: nil, want: false},
		{name: "codex profile alone", kv: map[string]string{"agent_profile": "codex"}, want: false},
		{name: "prompt opt in", kv: map[string]string{"mailbox_wake": "prompt"}, want: true},
		{name: "on opt in", kv: map[string]string{"mailbox_wake": "on"}, want: true},
		{name: "disabled beats codex profile", kv: map[string]string{"agent_profile": "codex", "mailbox_wake": "off"}, want: false},
		{name: "unknown value", kv: map[string]string{"mailbox_wake": "maybe"}, want: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := mailboxPaneWakeEnabled(tt.kv); got != tt.want {
				t.Fatalf("mailboxPaneWakeEnabled(%v) = %t, want %t", tt.kv, got, tt.want)
			}
		})
	}
}

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
