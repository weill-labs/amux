package server

import (
	"sort"
	"time"

	"github.com/weill-labs/amux/internal/mailbox"
	"github.com/weill-labs/amux/internal/proto"
)

const captureMailboxLatestUnreadLimit = 5

func (s *Session) ensureMailbox() *mailbox.Store {
	if s.mailbox == nil {
		s.mailbox = mailbox.NewStore(mailbox.Options{
			Now: func() time.Time {
				return s.clock().Now()
			},
		})
	}
	return s.mailbox
}

func (s *Session) mailboxCaptureSummary(paneID uint32) *proto.CaptureMailbox {
	if paneID == 0 || s.mailbox == nil {
		return proto.EmptyCaptureMailbox()
	}
	unread, err := s.mailbox.ListUnread(paneID)
	if err != nil {
		return proto.EmptyCaptureMailbox()
	}
	return mailboxCaptureSummaryFromUnread(unread)
}

func mailboxCaptureSummaryFromUnread(unread []mailbox.DeliverySummary) *proto.CaptureMailbox {
	summary := proto.EmptyCaptureMailbox()
	summary.Unread = len(unread)
	if len(unread) == 0 {
		return summary
	}

	for _, delivery := range unread {
		for _, topic := range delivery.Topics {
			summary.Topics[topic]++
		}
	}

	latest := append([]mailbox.DeliverySummary(nil), unread...)
	sort.SliceStable(latest, func(i, j int) bool {
		if latest[i].CreatedAt.Equal(latest[j].CreatedAt) {
			return latest[i].MessageID > latest[j].MessageID
		}
		return latest[i].CreatedAt.After(latest[j].CreatedAt)
	})
	if len(latest) > captureMailboxLatestUnreadLimit {
		latest = latest[:captureMailboxLatestUnreadLimit]
	}
	summary.LatestUnread = make([]proto.CaptureMailboxMessage, 0, len(latest))
	for _, delivery := range latest {
		summary.LatestUnread = append(summary.LatestUnread, mailboxCaptureMessage(delivery))
	}
	return summary
}

func mailboxCaptureMessage(delivery mailbox.DeliverySummary) proto.CaptureMailboxMessage {
	return proto.CaptureMailboxMessage{
		ID: string(delivery.MessageID),
		From: proto.MailboxAddress{
			ID:   delivery.Sender.ID,
			Name: delivery.Sender.Name,
			Host: delivery.Sender.Host,
		},
		Subject:     delivery.Subject,
		Topics:      append([]string(nil), delivery.Topics...),
		Groups:      append([]string(nil), delivery.Groups...),
		ThreadID:    string(delivery.ThreadID),
		InReplyTo:   string(delivery.InReplyTo),
		CreatedAt:   formatMailboxTime(delivery.CreatedAt),
		DeliveredAt: formatMailboxTime(delivery.DeliveredAt),
		BodySize:    delivery.BodySize,
		PartCount:   delivery.PartCount,
	}
}

func formatMailboxTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func (s *Session) applyMailboxSummariesToPaneSnapshots(panes []proto.PaneSnapshot) {
	for i := range panes {
		panes[i].Mailbox = s.mailboxCaptureSummary(panes[i].ID)
	}
}

func (s *Session) applyMailboxSummariesToLayout(snap *proto.LayoutSnapshot) {
	if snap == nil {
		return
	}
	s.applyMailboxSummariesToPaneSnapshots(snap.Panes)
	for wi := range snap.Windows {
		s.applyMailboxSummariesToPaneSnapshots(snap.Windows[wi].Panes)
	}
}
