package server

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/weill-labs/amux/internal/eventloop"
	"github.com/weill-labs/amux/internal/mailbox"
	"github.com/weill-labs/amux/internal/proto"
)

func (s *Session) enqueueMailboxSend(ctx context.Context, req mailbox.SendRequest) (mailbox.Message, error) {
	var msg mailbox.Message
	res := s.enqueueCommandMutationContext(ctx, func(mctx *MutationContext) commandMutationResult {
		var err error
		msg, err = mctx.sess.sendMailboxMessage(req)
		return commandMutationResult{err: err}
	})
	return msg, res.err
}

func (s *Session) enqueueMailboxRead(ctx context.Context, id mailbox.MessageID, recipientID uint32, opts mailbox.ReadOptions) (mailbox.Message, mailbox.DeliveryState, error) {
	var msg mailbox.Message
	var state mailbox.DeliveryState
	res := s.enqueueCommandMutationContext(ctx, func(mctx *MutationContext) commandMutationResult {
		var err error
		msg, state, err = mctx.sess.readMailboxMessage(id, recipientID, opts)
		return commandMutationResult{err: err}
	})
	return msg, state, res.err
}

func (s *Session) enqueueMailboxAck(ctx context.Context, id mailbox.MessageID, recipientID uint32, req mailbox.AckRequest) (mailbox.DeliveryState, error) {
	var state mailbox.DeliveryState
	res := s.enqueueCommandMutationContext(ctx, func(mctx *MutationContext) commandMutationResult {
		var err error
		state, err = mctx.sess.ackMailboxMessage(id, recipientID, req)
		return commandMutationResult{err: err}
	})
	return state, res.err
}

func (s *Session) sendMailboxMessage(req mailbox.SendRequest) (mailbox.Message, error) {
	msg, err := s.ensureMailbox().Send(req)
	if err != nil {
		return mailbox.Message{}, err
	}
	for _, recipient := range msg.Recipients {
		summary, err := s.mailbox.DeliverySummary(msg.ID, recipient.ID)
		if err != nil {
			return mailbox.Message{}, err
		}
		s.emitMailboxEvent(EventMessageDelivered, summary)
	}
	return msg, nil
}

func (s *Session) readMailboxMessage(id mailbox.MessageID, recipientID uint32, opts mailbox.ReadOptions) (mailbox.Message, mailbox.DeliveryState, error) {
	before, err := s.ensureMailbox().DeliverySummary(id, recipientID)
	if err != nil {
		return mailbox.Message{}, mailbox.DeliveryState{}, err
	}
	msg, state, err := s.mailbox.Read(id, recipientID, opts)
	if err != nil {
		return mailbox.Message{}, mailbox.DeliveryState{}, err
	}
	if !opts.Peek && before.ReadAt.IsZero() && !state.ReadAt.IsZero() {
		summary, err := s.mailbox.DeliverySummary(id, recipientID)
		if err != nil {
			return mailbox.Message{}, mailbox.DeliveryState{}, err
		}
		s.emitMailboxEvent(EventMessageRead, summary)
	}
	return msg, state, nil
}

func (s *Session) ackMailboxMessage(id mailbox.MessageID, recipientID uint32, req mailbox.AckRequest) (mailbox.DeliveryState, error) {
	before, err := s.ensureMailbox().DeliverySummary(id, recipientID)
	if err != nil {
		return mailbox.DeliveryState{}, err
	}
	state, err := s.mailbox.Ack(id, recipientID, req)
	if err != nil {
		return mailbox.DeliveryState{}, err
	}
	if before.AckedAt.IsZero() || before.AckStatus != req.Status || before.AckNote != req.Note {
		summary, err := s.mailbox.DeliverySummary(id, recipientID)
		if err != nil {
			return mailbox.DeliveryState{}, err
		}
		s.emitMailboxEvent(EventMessageAck, summary)
	}
	return state, nil
}

func (s *Session) emitMailboxEvent(eventType string, summary mailbox.DeliverySummary) {
	seq := s.nextMailboxEventSeq()
	if updated, err := s.ensureMailbox().SetLastEventSeq(summary.MessageID, summary.Recipient.ID, seq); err == nil {
		summary = updated
	}
	s.emitEvent(mailboxEvent(eventType, seq, summary))
}

func (s *Session) nextMailboxEventSeq() uint64 {
	s.mailboxEventSeq++
	return s.mailboxEventSeq
}

func (s *Session) currentMailboxStateEvents(now string) []Event {
	if s.mailbox == nil {
		return nil
	}
	var events []Event
	for _, pane := range s.Panes {
		summaries, err := s.mailbox.ListUnread(pane.ID)
		if err != nil {
			continue
		}
		for _, summary := range summaries {
			ev := mailboxEvent(EventMessageDelivered, summary.LastEventSeq, summary)
			ev.Timestamp = now
			events = append(events, ev)
		}
	}
	return events
}

func mailboxEvent(eventType string, seq uint64, summary mailbox.DeliverySummary) Event {
	return Event{
		Type:       eventType,
		Generation: seq,
		PaneID:     summary.Recipient.ID,
		PaneName:   summary.Recipient.Name,
		Host:       summary.Recipient.Host,
		Message:    mailboxSummaryToProto(summary),
	}
}

func mailboxSummaryToProto(summary mailbox.DeliverySummary) *proto.MailboxMessageSummary {
	return &proto.MailboxMessageSummary{
		ID:           string(summary.MessageID),
		From:         mailboxAddressToProto(summary.Sender),
		Subject:      summary.Subject,
		Topics:       append([]string(nil), summary.Topics...),
		Groups:       append([]string(nil), summary.Groups...),
		ThreadID:     string(summary.ThreadID),
		InReplyTo:    string(summary.InReplyTo),
		BodySize:     summary.BodySize,
		PartCount:    summary.PartCount,
		CreatedAt:    formatMailboxTime(summary.CreatedAt),
		DeliveredAt:  formatMailboxTime(summary.DeliveredAt),
		ReadAt:       formatMailboxTime(summary.ReadAt),
		AckedAt:      formatMailboxTime(summary.AckedAt),
		AckStatus:    summary.AckStatus,
		LastEventSeq: summary.LastEventSeq,
	}
}

func mailboxAddressToProto(addr mailbox.PaneAddress) proto.MailboxAddress {
	return proto.MailboxAddress{
		ID:   addr.ID,
		Name: addr.Name,
		Host: addr.Host,
	}
}

type mailboxWaitSubscribeResult struct {
	sub          *eventSub
	initialState [][]byte
	targetExists bool
}

type mailboxWaitOptions struct {
	topic          string
	afterMessageID string
	afterEventSeq  uint64
}

type mailboxWaitSubscribeCmd struct {
	paneID uint32
	reply  chan mailboxWaitSubscribeResult
}

func (e mailboxWaitSubscribeCmd) handle(ctx context.Context, s *Session) {
	filter := eventFilter{
		Types:  []string{EventMessageDelivered, EventPaneExit},
		PaneID: e.paneID,
	}
	sub := eventloop.Subscribe(&s.eventSubs, filter)
	result := mailboxWaitSubscribeResult{
		sub:          sub,
		initialState: eventloop.MarshalMatching(s.currentStateEvents(), filter),
		targetExists: s.findPaneByID(e.paneID) != nil && s.findWindowByPaneID(e.paneID) != nil,
	}
	select {
	case e.reply <- result:
	case <-ctx.Done():
		eventloop.Unsubscribe(&s.eventSubs, sub)
	}
}

func (s *Session) enqueueMailboxWaitSubscribe(ctx context.Context, paneID uint32) mailboxWaitSubscribeResult {
	reply := make(chan mailboxWaitSubscribeResult)
	if !s.enqueueEvent(ctx, mailboxWaitSubscribeCmd{paneID: paneID, reply: reply}) {
		return mailboxWaitSubscribeResult{}
	}
	select {
	case res := <-reply:
		return res
	case <-ctx.Done():
		return mailboxWaitSubscribeResult{}
	case <-s.sessionEventDone:
		return mailboxWaitSubscribeResult{}
	}
}

func mailboxEventSummaryFromJSON(data []byte) (Event, bool) {
	var ev Event
	if err := json.Unmarshal(data, &ev); err != nil {
		return Event{}, false
	}
	return ev, true
}

func mailboxWaitEventFromJSON(data []byte, opts mailboxWaitOptions) (summary proto.MailboxMessageSummary, matched bool, paneExited bool) {
	ev, ok := mailboxEventSummaryFromJSON(data)
	if !ok {
		return proto.MailboxMessageSummary{}, false, false
	}
	if ev.Type == EventPaneExit {
		return proto.MailboxMessageSummary{}, false, true
	}
	if !mailboxEventMatchesWait(ev, opts) {
		return proto.MailboxMessageSummary{}, false, false
	}
	return *ev.Message, true, false
}

func mailboxEventMatchesWait(ev Event, opts mailboxWaitOptions) bool {
	if ev.Type != EventMessageDelivered || ev.Message == nil {
		return false
	}
	if opts.topic != "" && !slices.Contains(ev.Message.Topics, opts.topic) {
		return false
	}
	if opts.afterMessageID != "" && ev.Message.ID <= opts.afterMessageID {
		return false
	}
	if opts.afterEventSeq != 0 && ev.Generation <= opts.afterEventSeq {
		return false
	}
	return true
}
