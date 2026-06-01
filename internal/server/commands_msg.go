package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/weill-labs/amux/internal/mailbox"
	"github.com/weill-labs/amux/internal/mux"
)

const (
	msgUsage             = "usage: msg <send|reply|inbox|drain-status|read|ack> ..."
	msgSendUsage         = "usage: msg send [--from pane] --to pane[,pane...] [--subject text] [--topic name] [--group name] [--metadata json] [--reply-to msg-id] --body text [--format json]"
	msgReplyUsage        = "usage: msg reply <msg-id> [--from pane] [--to pane[,pane...]] [--subject text] [--topic name] [--group name] [--metadata json] [--ack status] [--ack-note text] --body text [--format json]"
	msgInboxUsage        = "usage: msg inbox [pane] [--unread] [--format json]"
	msgDrainStatusUsage  = "usage: msg drain-status [pane] [--format json]"
	msgReadUsage         = "usage: msg read <msg-id> [--for pane] [--peek] [--format json]"
	msgAckUsage          = "usage: msg ack <msg-id> [--for pane] [--status ok|error|seen] [--note text] [--format json]"
	msgDrainLatestLimit  = 5
	msgSubjectBriefLimit = 120
)

type msgFormat string

const (
	msgFormatText msgFormat = "text"
	msgFormatJSON msgFormat = "json"
)

type msgSendOptions struct {
	from     string
	to       []string
	subject  string
	body     []byte
	topics   []string
	groups   []string
	metadata map[string]json.RawMessage
	replyTo  mailbox.MessageID
	format   msgFormat
}

type msgInboxOptions struct {
	target     string
	unreadOnly bool
	format     msgFormat
}

type msgDrainStatusOptions struct {
	target string
	format msgFormat
}

type msgReplyOptions struct {
	id        mailbox.MessageID
	from      string
	to        []string
	subject   string
	body      []byte
	topics    []string
	groups    []string
	metadata  map[string]json.RawMessage
	ackStatus string
	ackNote   string
	format    msgFormat
}

type msgReadOptions struct {
	id     mailbox.MessageID
	target string
	peek   bool
	format msgFormat
}

type msgAckOptions struct {
	id     mailbox.MessageID
	target string
	status string
	note   string
	format msgFormat
}

type msgSendOutput struct {
	ID         mailbox.MessageID     `json:"id"`
	Sender     mailbox.PaneAddress   `json:"sender"`
	Recipients []mailbox.PaneAddress `json:"recipients"`
	Subject    string                `json:"subject"`
	Topics     []string              `json:"topics,omitempty"`
	Groups     []string              `json:"groups,omitempty"`
	ThreadID   mailbox.ThreadID      `json:"thread_id"`
	InReplyTo  mailbox.MessageID     `json:"in_reply_to,omitempty"`
	CreatedAt  string                `json:"created_at"`
	BodySize   int                   `json:"body_size"`
	PartCount  int                   `json:"part_count"`
}

type msgSummaryOutput struct {
	ID          mailbox.MessageID   `json:"id"`
	Sender      mailbox.PaneAddress `json:"sender"`
	Recipient   mailbox.PaneAddress `json:"recipient"`
	Subject     string              `json:"subject"`
	Topics      []string            `json:"topics,omitempty"`
	Groups      []string            `json:"groups,omitempty"`
	ThreadID    mailbox.ThreadID    `json:"thread_id"`
	InReplyTo   mailbox.MessageID   `json:"in_reply_to,omitempty"`
	CreatedAt   string              `json:"created_at"`
	DeliveredAt string              `json:"delivered_at"`
	ReadAt      string              `json:"read_at,omitempty"`
	AckedAt     string              `json:"acked_at,omitempty"`
	AckStatus   string              `json:"ack_status,omitempty"`
	AckNote     string              `json:"ack_note,omitempty"`
	BodySize    int                 `json:"body_size"`
	PartCount   int                 `json:"part_count"`
}

type msgDrainStatusOutput struct {
	Unread             int                 `json:"unread"`
	Unacked            int                 `json:"unacked"`
	Pending            int                 `json:"pending"`
	PendingFingerprint string              `json:"pending_fingerprint"`
	PendingIDs         []mailbox.MessageID `json:"pending_ids"`
	Latest             []msgSummaryOutput  `json:"latest"`
}

type msgReadOutput struct {
	ID         mailbox.MessageID          `json:"id"`
	Sender     mailbox.PaneAddress        `json:"sender"`
	Recipients []mailbox.PaneAddress      `json:"recipients"`
	Subject    string                     `json:"subject"`
	Topics     []string                   `json:"topics,omitempty"`
	Groups     []string                   `json:"groups,omitempty"`
	ThreadID   mailbox.ThreadID           `json:"thread_id"`
	InReplyTo  mailbox.MessageID          `json:"in_reply_to,omitempty"`
	CreatedAt  string                     `json:"created_at"`
	ReadAt     string                     `json:"read_at,omitempty"`
	Body       string                     `json:"body"`
	BodySize   int                        `json:"body_size"`
	PartCount  int                        `json:"part_count"`
	Delivery   mailbox.DeliveryState      `json:"delivery"`
	Metadata   map[string]json.RawMessage `json:"metadata,omitempty"`
}

type msgAckOutput struct {
	MessageID mailbox.MessageID     `json:"id"`
	Delivery  mailbox.DeliveryState `json:"delivery"`
}

func cmdMsg(ctx *CommandContext) {
	if len(ctx.Args) == 0 {
		ctx.replyErr(msgUsage)
		return
	}

	switch ctx.Args[0] {
	case "send":
		opts, err := parseMsgSendOptions(ctx.Args[1:])
		if err != nil {
			ctx.replyErr(err.Error())
			return
		}
		ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutationContext(ctx.context(), func(mctx *MutationContext) commandMutationResult {
			output, err := runMsgSend(mctx, ctx.ActorPaneID, opts)
			return commandMutationResult{output: output, err: err}
		}))
	case "reply":
		opts, err := parseMsgReplyOptions(ctx.Args[1:])
		if err != nil {
			ctx.replyErr(err.Error())
			return
		}
		ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutationContext(ctx.context(), func(mctx *MutationContext) commandMutationResult {
			output, err := runMsgReply(mctx, ctx.ActorPaneID, opts)
			return commandMutationResult{output: output, err: err}
		}))
	case "inbox":
		opts, err := parseMsgInboxOptions(ctx.Args[1:])
		if err != nil {
			ctx.replyErr(err.Error())
			return
		}
		ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutationContext(ctx.context(), func(mctx *MutationContext) commandMutationResult {
			output, err := runMsgInbox(mctx, ctx.ActorPaneID, opts)
			return commandMutationResult{output: output, err: err}
		}))
	case "drain-status":
		opts, err := parseMsgDrainStatusOptions(ctx.Args[1:])
		if err != nil {
			ctx.replyErr(err.Error())
			return
		}
		ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutationContext(ctx.context(), func(mctx *MutationContext) commandMutationResult {
			output, err := runMsgDrainStatus(mctx, ctx.ActorPaneID, opts)
			return commandMutationResult{output: output, err: err}
		}))
	case "read":
		opts, err := parseMsgReadOptions(ctx.Args[1:])
		if err != nil {
			ctx.replyErr(err.Error())
			return
		}
		ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutationContext(ctx.context(), func(mctx *MutationContext) commandMutationResult {
			output, err := runMsgRead(mctx, ctx.ActorPaneID, opts)
			return commandMutationResult{output: output, err: err}
		}))
	case "ack":
		opts, err := parseMsgAckOptions(ctx.Args[1:])
		if err != nil {
			ctx.replyErr(err.Error())
			return
		}
		ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutationContext(ctx.context(), func(mctx *MutationContext) commandMutationResult {
			output, err := runMsgAck(mctx, ctx.ActorPaneID, opts)
			return commandMutationResult{output: output, err: err}
		}))
	default:
		ctx.replyErr(msgUsage)
	}
}

func parseMsgSendOptions(args []string) (msgSendOptions, error) {
	opts := msgSendOptions{format: msgFormatText}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--from":
			value, next, err := requiredFlagValue(args, i, "--from")
			if err != nil {
				return opts, err
			}
			opts.from = value
			i = next
		case "--to":
			value, next, err := requiredFlagValue(args, i, "--to")
			if err != nil {
				return opts, err
			}
			opts.to = appendCSVValues(opts.to, value)
			i = next
		case "--subject":
			value, next, err := requiredFlagValue(args, i, "--subject")
			if err != nil {
				return opts, err
			}
			opts.subject = value
			i = next
		case "--body":
			value, next, err := requiredFlagValue(args, i, "--body")
			if err != nil {
				return opts, err
			}
			opts.body = []byte(value)
			i = next
		case "--topic":
			value, next, err := requiredFlagValue(args, i, "--topic")
			if err != nil {
				return opts, err
			}
			opts.topics = appendCSVValues(opts.topics, value)
			i = next
		case "--group":
			value, next, err := requiredFlagValue(args, i, "--group")
			if err != nil {
				return opts, err
			}
			opts.groups = appendCSVValues(opts.groups, value)
			i = next
		case "--metadata":
			value, next, err := requiredFlagValue(args, i, "--metadata")
			if err != nil {
				return opts, err
			}
			metadata, err := parseMsgMetadata(value)
			if err != nil {
				return opts, err
			}
			opts.metadata = metadata
			i = next
		case "--reply-to":
			value, next, err := requiredFlagValue(args, i, "--reply-to")
			if err != nil {
				return opts, err
			}
			opts.replyTo = mailbox.MessageID(value)
			i = next
		case "--format":
			format, next, err := parseMsgFormatFlag(args, i)
			if err != nil {
				return opts, err
			}
			opts.format = format
			i = next
		default:
			return opts, errors.New(msgSendUsage)
		}
	}
	return opts, nil
}

func parseMsgReplyOptions(args []string) (msgReplyOptions, error) {
	opts := msgReplyOptions{format: msgFormatText}
	if len(args) == 0 {
		return opts, errors.New(msgReplyUsage)
	}
	opts.id = mailbox.MessageID(args[0])
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--from":
			value, next, err := requiredFlagValue(args, i, "--from")
			if err != nil {
				return opts, err
			}
			opts.from = value
			i = next
		case "--to":
			value, next, err := requiredFlagValue(args, i, "--to")
			if err != nil {
				return opts, err
			}
			opts.to = appendCSVValues(opts.to, value)
			i = next
		case "--subject":
			value, next, err := requiredFlagValue(args, i, "--subject")
			if err != nil {
				return opts, err
			}
			opts.subject = value
			i = next
		case "--body":
			value, next, err := requiredFlagValue(args, i, "--body")
			if err != nil {
				return opts, err
			}
			opts.body = []byte(value)
			i = next
		case "--topic":
			value, next, err := requiredFlagValue(args, i, "--topic")
			if err != nil {
				return opts, err
			}
			opts.topics = appendCSVValues(opts.topics, value)
			i = next
		case "--group":
			value, next, err := requiredFlagValue(args, i, "--group")
			if err != nil {
				return opts, err
			}
			opts.groups = appendCSVValues(opts.groups, value)
			i = next
		case "--metadata":
			value, next, err := requiredFlagValue(args, i, "--metadata")
			if err != nil {
				return opts, err
			}
			metadata, err := parseMsgMetadata(value)
			if err != nil {
				return opts, err
			}
			opts.metadata = metadata
			i = next
		case "--ack":
			value, next, err := requiredFlagValue(args, i, "--ack")
			if err != nil {
				return opts, err
			}
			opts.ackStatus = value
			i = next
		case "--ack-note":
			value, next, err := requiredFlagValue(args, i, "--ack-note")
			if err != nil {
				return opts, err
			}
			opts.ackNote = value
			i = next
		case "--format":
			format, next, err := parseMsgFormatFlag(args, i)
			if err != nil {
				return opts, err
			}
			opts.format = format
			i = next
		default:
			return opts, errors.New(msgReplyUsage)
		}
	}
	return opts, nil
}

func parseMsgInboxOptions(args []string) (msgInboxOptions, error) {
	opts := msgInboxOptions{format: msgFormatText}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--unread":
			opts.unreadOnly = true
		case "--format":
			format, next, err := parseMsgFormatFlag(args, i)
			if err != nil {
				return opts, err
			}
			opts.format = format
			i = next
		default:
			if strings.HasPrefix(args[i], "-") {
				return opts, errors.New(msgInboxUsage)
			}
			if opts.target != "" {
				return opts, errors.New(msgInboxUsage)
			}
			opts.target = args[i]
		}
	}
	return opts, nil
}

func parseMsgDrainStatusOptions(args []string) (msgDrainStatusOptions, error) {
	opts := msgDrainStatusOptions{format: msgFormatText}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--format":
			format, next, err := parseMsgFormatFlag(args, i)
			if err != nil {
				return opts, err
			}
			opts.format = format
			i = next
		default:
			if strings.HasPrefix(args[i], "-") {
				return opts, errors.New(msgDrainStatusUsage)
			}
			if opts.target != "" {
				return opts, errors.New(msgDrainStatusUsage)
			}
			opts.target = args[i]
		}
	}
	return opts, nil
}

func parseMsgReadOptions(args []string) (msgReadOptions, error) {
	opts := msgReadOptions{format: msgFormatText}
	if len(args) == 0 {
		return opts, errors.New(msgReadUsage)
	}
	opts.id = mailbox.MessageID(args[0])
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--for":
			value, next, err := requiredFlagValue(args, i, "--for")
			if err != nil {
				return opts, err
			}
			opts.target = value
			i = next
		case "--peek":
			opts.peek = true
		case "--format":
			format, next, err := parseMsgFormatFlag(args, i)
			if err != nil {
				return opts, err
			}
			opts.format = format
			i = next
		default:
			return opts, errors.New(msgReadUsage)
		}
	}
	return opts, nil
}

func parseMsgAckOptions(args []string) (msgAckOptions, error) {
	opts := msgAckOptions{format: msgFormatText}
	if len(args) == 0 {
		return opts, errors.New(msgAckUsage)
	}
	opts.id = mailbox.MessageID(args[0])
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--for":
			value, next, err := requiredFlagValue(args, i, "--for")
			if err != nil {
				return opts, err
			}
			opts.target = value
			i = next
		case "--status":
			value, next, err := requiredFlagValue(args, i, "--status")
			if err != nil {
				return opts, err
			}
			opts.status = value
			i = next
		case "--note":
			value, next, err := requiredFlagValue(args, i, "--note")
			if err != nil {
				return opts, err
			}
			opts.note = value
			i = next
		case "--format":
			format, next, err := parseMsgFormatFlag(args, i)
			if err != nil {
				return opts, err
			}
			opts.format = format
			i = next
		default:
			return opts, errors.New(msgAckUsage)
		}
	}
	return opts, nil
}

func requiredFlagValue(args []string, i int, name string) (string, int, error) {
	if i+1 >= len(args) {
		return "", i, fmt.Errorf("missing value for %s", name)
	}
	return args[i+1], i + 1, nil
}

func parseMsgFormatFlag(args []string, i int) (msgFormat, int, error) {
	value, next, err := requiredFlagValue(args, i, "--format")
	if err != nil {
		return "", i, err
	}
	switch value {
	case "json":
		return msgFormatJSON, next, nil
	case "text":
		return msgFormatText, next, nil
	default:
		return "", i, fmt.Errorf("unsupported msg format %q", value)
	}
}

func appendCSVValues(out []string, raw string) []string {
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func parseMsgMetadata(raw string) (map[string]json.RawMessage, error) {
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		return nil, fmt.Errorf("invalid metadata JSON: %w", err)
	}
	if metadata == nil && strings.TrimSpace(raw) != "{}" {
		return nil, fmt.Errorf("metadata must be a JSON object")
	}
	return metadata, nil
}

func runMsgSend(mctx *MutationContext, actorPaneID uint32, opts msgSendOptions) (string, error) {
	sender, err := resolveMailboxSender(mctx, actorPaneID, opts.from)
	if err != nil {
		return "", err
	}
	recipients, err := resolveMailboxRecipients(mctx, actorPaneID, opts.to)
	if err != nil {
		return "", err
	}
	msg, err := mctx.sess.sendMailboxMessage(mailbox.SendRequest{
		Sender:     sender,
		Recipients: recipients,
		Topics:     opts.topics,
		Groups:     opts.groups,
		Subject:    opts.subject,
		Body:       opts.body,
		Metadata:   opts.metadata,
		ReplyTo:    opts.replyTo,
	})
	if err != nil {
		return "", err
	}
	if opts.format == msgFormatJSON {
		return encodeMsgJSON(sendOutputForMessage(msg))
	}
	return fmt.Sprintf("Sent %s to %s\n", msg.ID, joinPaneNames(msg.Recipients)), nil
}

func runMsgInbox(mctx *MutationContext, actorPaneID uint32, opts msgInboxOptions) (string, error) {
	recipient, err := resolveMailboxTarget(mctx, actorPaneID, opts.target, "inbox target")
	if err != nil {
		return "", err
	}
	summaries, err := mctx.sess.ensureMailbox().List(recipient.ID, mailbox.ListOptions{UnreadOnly: opts.unreadOnly})
	if err != nil {
		return "", err
	}
	if opts.format == msgFormatJSON {
		return encodeMsgJSON(summariesOutput(summaries))
	}
	return formatMsgInboxText(summaries), nil
}

func runMsgDrainStatus(mctx *MutationContext, actorPaneID uint32, opts msgDrainStatusOptions) (string, error) {
	recipient, err := resolveMailboxTarget(mctx, actorPaneID, opts.target, "drain-status target")
	if err != nil {
		return "", err
	}
	status, err := mctx.sess.ensureMailbox().DrainStatus(recipient.ID, mailbox.DrainOptions{LatestLimit: msgDrainLatestLimit})
	if err != nil {
		return "", err
	}
	if opts.format == msgFormatJSON {
		return encodeMsgJSON(drainStatusOutput(status))
	}
	return fmt.Sprintf("%d\n", status.Pending), nil
}

func runMsgReply(mctx *MutationContext, actorPaneID uint32, opts msgReplyOptions) (string, error) {
	sender, err := resolveMailboxSender(mctx, actorPaneID, opts.from)
	if err != nil {
		return "", err
	}
	parent, ok := mctx.sess.ensureMailbox().Message(opts.id)
	if !ok {
		return "", fmt.Errorf("message %q not found", opts.id)
	}
	recipients, err := resolveMailboxReplyRecipients(mctx, actorPaneID, sender, parent, opts.to)
	if err != nil {
		return "", err
	}
	if opts.ackStatus != "" || opts.ackNote != "" {
		if _, err := mctx.sess.ensureMailbox().DeliverySummary(parent.ID, sender.ID); err != nil {
			return "", fmt.Errorf("cannot ack reply parent as %s: %w", sender.Name, err)
		}
	}
	topics := opts.topics
	if len(topics) == 0 {
		topics = parent.Topics
	}
	groups := opts.groups
	if len(groups) == 0 {
		groups = parent.Groups
	}
	msg, err := mctx.sess.sendMailboxMessage(mailbox.SendRequest{
		Sender:     sender,
		Recipients: recipients,
		Topics:     topics,
		Groups:     groups,
		Subject:    opts.subject,
		Body:       opts.body,
		Metadata:   opts.metadata,
		ReplyTo:    parent.ID,
	})
	if err != nil {
		return "", err
	}
	if opts.ackStatus != "" || opts.ackNote != "" {
		if _, err := mctx.sess.ackMailboxMessage(parent.ID, sender.ID, mailbox.AckRequest{Status: opts.ackStatus, Note: opts.ackNote}); err != nil {
			return "", err
		}
	}
	if opts.format == msgFormatJSON {
		return encodeMsgJSON(sendOutputForMessage(msg))
	}
	return fmt.Sprintf("Sent %s to %s\n", msg.ID, joinPaneNames(msg.Recipients)), nil
}

func runMsgRead(mctx *MutationContext, actorPaneID uint32, opts msgReadOptions) (string, error) {
	recipient, err := resolveMailboxTarget(mctx, actorPaneID, opts.target, "read target")
	if err != nil {
		return "", err
	}
	msg, delivery, err := mctx.sess.readMailboxMessage(opts.id, recipient.ID, mailbox.ReadOptions{Peek: opts.peek})
	if err != nil {
		return "", err
	}
	body := msgBodyText(msg)
	if opts.format == msgFormatJSON {
		return encodeMsgJSON(readOutputForMessage(msg, delivery, body))
	}
	return ensureMsgTrailingNewline(body), nil
}

func runMsgAck(mctx *MutationContext, actorPaneID uint32, opts msgAckOptions) (string, error) {
	recipient, err := resolveMailboxTarget(mctx, actorPaneID, opts.target, "ack target")
	if err != nil {
		return "", err
	}
	delivery, err := mctx.sess.ackMailboxMessage(opts.id, recipient.ID, mailbox.AckRequest{Status: opts.status, Note: opts.note})
	if err != nil {
		return "", err
	}
	if opts.format == msgFormatJSON {
		return encodeMsgJSON(msgAckOutput{MessageID: opts.id, Delivery: delivery})
	}
	if opts.status != "" {
		return fmt.Sprintf("Acked %s for %s status=%s\n", opts.id, recipient.Name, opts.status), nil
	}
	return fmt.Sprintf("Acked %s for %s\n", opts.id, recipient.Name), nil
}

func resolveMailboxReplyRecipients(mctx *MutationContext, actorPaneID uint32, sender mailbox.PaneAddress, parent mailbox.Message, refs []string) ([]mailbox.PaneAddress, error) {
	if len(refs) > 0 {
		return resolveMailboxRecipients(mctx, actorPaneID, refs)
	}
	if sender.ID != parent.Sender.ID {
		if !messageHasRecipient(parent, sender.ID) {
			return nil, fmt.Errorf("reply recipient could not be inferred for %s; pass --to", sender.Name)
		}
		return []mailbox.PaneAddress{parent.Sender}, nil
	}
	recipients := make([]mailbox.PaneAddress, 0, len(parent.Recipients))
	for _, recipient := range parent.Recipients {
		if recipient.ID != sender.ID {
			recipients = append(recipients, recipient)
		}
	}
	if len(recipients) == 0 {
		return nil, fmt.Errorf("reply recipient could not be inferred for %s; pass --to", sender.Name)
	}
	return recipients, nil
}

func messageHasRecipient(msg mailbox.Message, paneID uint32) bool {
	for _, recipient := range msg.Recipients {
		if recipient.ID == paneID {
			return true
		}
	}
	return false
}

func resolveMailboxSender(mctx *MutationContext, actorPaneID uint32, ref string) (mailbox.PaneAddress, error) {
	if ref == "" {
		return mailboxActorAddress(mctx, actorPaneID, "sender")
	}
	return resolveMailboxPaneRef(mctx, actorPaneID, ref, "sender")
}

func resolveMailboxTarget(mctx *MutationContext, actorPaneID uint32, ref, role string) (mailbox.PaneAddress, error) {
	if ref == "" {
		return mailboxActorAddress(mctx, actorPaneID, role)
	}
	return resolveMailboxPaneRef(mctx, actorPaneID, ref, role)
}

func resolveMailboxRecipients(mctx *MutationContext, actorPaneID uint32, refs []string) ([]mailbox.PaneAddress, error) {
	if len(refs) == 0 {
		return nil, fmt.Errorf("at least one recipient is required")
	}
	recipients := make([]mailbox.PaneAddress, 0, len(refs))
	for _, ref := range refs {
		recipient, err := resolveMailboxPaneRef(mctx, actorPaneID, ref, "recipient")
		if err != nil {
			return nil, err
		}
		recipients = append(recipients, recipient)
	}
	return recipients, nil
}

func mailboxActorAddress(mctx *MutationContext, actorPaneID uint32, role string) (mailbox.PaneAddress, error) {
	if actorPaneID == 0 {
		return mailbox.PaneAddress{}, fmt.Errorf("%s pane is required", role)
	}
	pane := mctx.findPaneByID(actorPaneID)
	if pane == nil {
		return mailbox.PaneAddress{}, fmt.Errorf("%s pane %d not found", role, actorPaneID)
	}
	if mctx.findWindowByPaneID(actorPaneID) == nil {
		return mailbox.PaneAddress{}, fmt.Errorf("%s pane %d is not in any window", role, actorPaneID)
	}
	return mailboxCommandAddressForPane(pane), nil
}

func resolveMailboxPaneRef(mctx *MutationContext, actorPaneID uint32, ref, role string) (mailbox.PaneAddress, error) {
	pane, window, err := mctx.resolvePaneAcrossWindowsForActor(actorPaneID, ref)
	if err != nil {
		return mailbox.PaneAddress{}, err
	}
	if window == nil {
		return mailbox.PaneAddress{}, fmt.Errorf("%s pane %q is not in any window", role, ref)
	}
	return mailboxCommandAddressForPane(pane), nil
}

func mailboxCommandAddressForPane(pane *mux.Pane) mailbox.PaneAddress {
	if pane == nil {
		return mailbox.PaneAddress{}
	}
	return mailbox.PaneAddress{ID: pane.ID, Name: pane.Meta.Name, Host: pane.Meta.Host}
}

func sendOutputForMessage(msg mailbox.Message) msgSendOutput {
	return msgSendOutput{
		ID:         msg.ID,
		Sender:     msg.Sender,
		Recipients: append([]mailbox.PaneAddress(nil), msg.Recipients...),
		Subject:    msg.Subject,
		Topics:     append([]string(nil), msg.Topics...),
		Groups:     append([]string(nil), msg.Groups...),
		ThreadID:   msg.ThreadID,
		InReplyTo:  msg.InReplyTo,
		CreatedAt:  msg.CreatedAt.Format(time.RFC3339Nano),
		BodySize:   messageOutputBodySize(msg),
		PartCount:  len(msg.Parts),
	}
}

func summariesOutput(summaries []mailbox.DeliverySummary) []msgSummaryOutput {
	out := make([]msgSummaryOutput, 0, len(summaries))
	for _, summary := range summaries {
		out = append(out, summaryOutput(summary))
	}
	return out
}

func drainStatusOutput(status mailbox.DrainStatus) msgDrainStatusOutput {
	return msgDrainStatusOutput{
		Unread:             status.Unread,
		Unacked:            status.Unacked,
		Pending:            status.Pending,
		PendingFingerprint: status.PendingFingerprint,
		PendingIDs:         append([]mailbox.MessageID(nil), status.PendingIDs...),
		Latest:             drainStatusLatestOutput(status.Latest),
	}
}

func drainStatusLatestOutput(summaries []mailbox.DeliverySummary) []msgSummaryOutput {
	out := summariesOutput(summaries)
	for i := range out {
		out[i].Subject = briefMsgSubject(out[i].Subject, msgSubjectBriefLimit)
	}
	return out
}

func summaryOutput(summary mailbox.DeliverySummary) msgSummaryOutput {
	out := msgSummaryOutput{
		ID:          summary.MessageID,
		Sender:      summary.Sender,
		Recipient:   summary.Recipient,
		Subject:     summary.Subject,
		Topics:      append([]string(nil), summary.Topics...),
		Groups:      append([]string(nil), summary.Groups...),
		ThreadID:    summary.ThreadID,
		InReplyTo:   summary.InReplyTo,
		CreatedAt:   summary.CreatedAt.Format(time.RFC3339Nano),
		DeliveredAt: summary.DeliveredAt.Format(time.RFC3339Nano),
		AckStatus:   summary.AckStatus,
		AckNote:     summary.AckNote,
		BodySize:    summary.BodySize,
		PartCount:   summary.PartCount,
	}
	if !summary.ReadAt.IsZero() {
		out.ReadAt = summary.ReadAt.Format(time.RFC3339Nano)
	}
	if !summary.AckedAt.IsZero() {
		out.AckedAt = summary.AckedAt.Format(time.RFC3339Nano)
	}
	return out
}

func briefMsgSubject(subject string, limit int) string {
	if limit <= 0 || len([]byte(subject)) <= limit {
		return subject
	}
	if limit <= 3 {
		return "..."
	}
	for len(subject) > 0 && len([]byte(subject)) > limit-3 {
		_, size := utf8.DecodeLastRuneInString(subject)
		subject = subject[:len(subject)-size]
	}
	return subject + "..."
}

func readOutputForMessage(msg mailbox.Message, delivery mailbox.DeliveryState, body string) msgReadOutput {
	out := msgReadOutput{
		ID:         msg.ID,
		Sender:     msg.Sender,
		Recipients: append([]mailbox.PaneAddress(nil), msg.Recipients...),
		Subject:    msg.Subject,
		Topics:     append([]string(nil), msg.Topics...),
		Groups:     append([]string(nil), msg.Groups...),
		ThreadID:   msg.ThreadID,
		InReplyTo:  msg.InReplyTo,
		CreatedAt:  msg.CreatedAt.Format(time.RFC3339Nano),
		Body:       body,
		BodySize:   messageOutputBodySize(msg),
		PartCount:  len(msg.Parts),
		Delivery:   delivery,
		Metadata:   msg.Metadata,
	}
	if !delivery.ReadAt.IsZero() {
		out.ReadAt = delivery.ReadAt.Format(time.RFC3339Nano)
	}
	return out
}

func encodeMsgJSON(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(data) + "\n", nil
}

func msgBodyText(msg mailbox.Message) string {
	var b strings.Builder
	for _, part := range msg.Parts {
		b.Write(part.Bytes)
	}
	return b.String()
}

func messageOutputBodySize(msg mailbox.Message) int {
	total := 0
	for _, part := range msg.Parts {
		total += part.Size
	}
	return total
}

func ensureMsgTrailingNewline(s string) string {
	if strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}

func joinPaneNames(addrs []mailbox.PaneAddress) string {
	names := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		names = append(names, addr.Name)
	}
	return strings.Join(names, ",")
}

func formatMsgInboxText(summaries []mailbox.DeliverySummary) string {
	if len(summaries) == 0 {
		return ""
	}
	var b strings.Builder
	for _, summary := range summaries {
		fmt.Fprintf(&b, "%s from %s: %s (%d bytes)\n", summary.MessageID, summary.Sender.Name, summary.Subject, summary.BodySize)
	}
	return b.String()
}
