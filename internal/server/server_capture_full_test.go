package server

import (
	"testing"

	"github.com/weill-labs/amux/internal/proto"
)

func TestServerPaneDataMailboxUnreadCount(t *testing.T) {
	t.Parallel()

	pane := &serverPaneData{
		pane: &serverCapturePane{
			info: proto.PaneSnapshot{
				Mailbox: &proto.CaptureMailbox{Unread: 6},
			},
		},
	}
	if got := pane.MailboxUnreadCount(); got != 6 {
		t.Fatalf("MailboxUnreadCount() = %d, want 6", got)
	}
	if got := (&serverPaneData{pane: &serverCapturePane{}}).MailboxUnreadCount(); got != 0 {
		t.Fatalf("nil mailbox MailboxUnreadCount() = %d, want 0", got)
	}
}
