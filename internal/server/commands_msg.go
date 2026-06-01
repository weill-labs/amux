package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/weill-labs/amux/internal/checkpoint"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mailbox"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/remote"
)

const (
	msgUsage             = "usage: msg <send|reply|inbox|list|drain-status|read|ack|thread> ..."
	msgSendUsage         = "usage: msg send [--from pane] --to pane[,pane...] [--subject text] [--topic name] [--group name] [--metadata json] [--reply-to msg-id] --body text [--format json]"
	msgReplyUsage        = "usage: msg reply <msg-id> [--from pane] [--to pane[,pane...]] [--subject text] [--topic name] [--group name] [--metadata json] [--ack status] [--ack-note text] --body text [--format json]"
	msgDeliverUsage      = "usage: msg deliver <payload-json> [--format json]"
	msgInboxUsage        = "usage: msg inbox|list [pane] [--unread] [--format json]"
	msgDrainStatusUsage  = "usage: msg drain-status [pane] [--format json]"
	msgReadUsage         = "usage: msg read <msg-id> [--for pane] [--peek] [--format json]"
	msgAckUsage          = "usage: msg ack <msg-id> [--for pane] [--status ok|error|seen] [--note text] [--format json]"
	msgThreadUsage       = "usage: msg thread <topic|msg-id> [--format json]"
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

type msgDeliverOptions struct {
	payload msgRemoteDeliverPayload
	format  msgFormat
}

type msgReadOptions struct {
	id     mailbox.MessageID
	target string
	peek   bool
	format msgFormat
}

type msgThreadOptions struct {
	ref    string
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
	From        mailbox.PaneAddress `json:"from"`
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
	From       mailbox.PaneAddress        `json:"from"`
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

type msgThreadMessageOutput struct {
	ID         mailbox.MessageID     `json:"id"`
	Sender     mailbox.PaneAddress   `json:"sender"`
	Recipients []mailbox.PaneAddress `json:"recipients"`
	Subject    string                `json:"subject"`
	Topics     []string              `json:"topics,omitempty"`
	Groups     []string              `json:"groups,omitempty"`
	ThreadID   mailbox.ThreadID      `json:"thread_id"`
	InReplyTo  mailbox.MessageID     `json:"in_reply_to,omitempty"`
	CreatedAt  string                `json:"created_at"`
	Body       string                `json:"body"`
	BodySize   int                   `json:"body_size"`
	PartCount  int                   `json:"part_count"`
}

type msgAckOutput struct {
	MessageID mailbox.MessageID     `json:"id"`
	Delivery  mailbox.DeliveryState `json:"delivery"`
}

type msgRecipientTarget struct {
	address   mailbox.PaneAddress
	remoteRef *checkpoint.RemoteRef
}

type msgRemoteRecipient struct {
	ref checkpoint.RemoteRef
}

type msgRemoteDeliverPayload struct {
	Sender     mailbox.PaneAddress        `json:"sender"`
	Recipients []string                   `json:"recipients"`
	Topics     []string                   `json:"topics,omitempty"`
	Groups     []string                   `json:"groups,omitempty"`
	Subject    string                     `json:"subject"`
	Body       []byte                     `json:"body"`
	Metadata   map[string]json.RawMessage `json:"metadata,omitempty"`
	ThreadID   mailbox.ThreadID           `json:"thread_id,omitempty"`
	ReplyTo    mailbox.MessageID          `json:"reply_to,omitempty"`
}

type msgSendPlan struct {
	sender           mailbox.PaneAddress
	localRecipients  []mailbox.PaneAddress
	remoteRecipients map[string][]msgRemoteRecipient
	remoteThreadID   mailbox.ThreadID
}

type msgReplyPlan struct {
	sender           mailbox.PaneAddress
	parent           mailbox.Message
	localRecipients  []mailbox.PaneAddress
	remoteRecipients map[string][]msgRemoteRecipient
	topics           []string
	groups           []string
	remoteThreadID   mailbox.ThreadID
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
		output, err := runMsgSendCommand(ctx, opts)
		ctx.replyCommandMutation(commandMutationResult{output: output, err: err})
	case "reply":
		opts, err := parseMsgReplyOptions(ctx.Args[1:])
		if err != nil {
			ctx.replyErr(err.Error())
			return
		}
		output, err := runMsgReplyCommand(ctx, opts)
		ctx.replyCommandMutation(commandMutationResult{output: output, err: err})
	case "deliver":
		opts, err := parseMsgDeliverOptions(ctx.Args[1:])
		if err != nil {
			ctx.replyErr(err.Error())
			return
		}
		ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutationContext(ctx.context(), func(mctx *MutationContext) commandMutationResult {
			output, err := runMsgDeliver(mctx, opts)
			return commandMutationResult{output: output, err: err}
		}))
	case "inbox", "list":
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
	case "thread":
		opts, err := parseMsgThreadOptions(ctx.Args[1:])
		if err != nil {
			ctx.replyErr(err.Error())
			return
		}
		ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutationContext(ctx.context(), func(mctx *MutationContext) commandMutationResult {
			output, err := runMsgThread(mctx, opts)
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

func parseMsgDeliverOptions(args []string) (msgDeliverOptions, error) {
	opts := msgDeliverOptions{format: msgFormatText}
	if len(args) == 0 {
		return opts, errors.New(msgDeliverUsage)
	}
	if err := json.Unmarshal([]byte(args[0]), &opts.payload); err != nil {
		return opts, fmt.Errorf("invalid deliver payload JSON: %w", err)
	}
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--format":
			format, next, err := parseMsgFormatFlag(args, i)
			if err != nil {
				return opts, err
			}
			opts.format = format
			i = next
		default:
			return opts, errors.New(msgDeliverUsage)
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

func parseMsgThreadOptions(args []string) (msgThreadOptions, error) {
	opts := msgThreadOptions{format: msgFormatText}
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
			if strings.HasPrefix(args[i], "-") || opts.ref != "" {
				return opts, errors.New(msgThreadUsage)
			}
			opts.ref = args[i]
		}
	}
	if opts.ref == "" {
		return opts, errors.New(msgThreadUsage)
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

func runMsgSendCommand(ctx *CommandContext, opts msgSendOptions) (string, error) {
	var plan msgSendPlan
	res := ctx.Sess.enqueueCommandMutationContext(ctx.context(), func(mctx *MutationContext) commandMutationResult {
		var err error
		plan, err = planMsgSend(mctx, ctx.ActorPaneID, opts)
		return commandMutationResult{err: err}
	})
	if res.err != nil {
		return "", res.err
	}

	remoteOutputs, err := forwardRemoteMailboxDeliveries(ctx, plan.sender, plan.remoteRecipients, msgRemoteDeliverPayload{
		Sender:   plan.sender,
		Topics:   opts.topics,
		Groups:   opts.groups,
		Subject:  opts.subject,
		Body:     opts.body,
		Metadata: opts.metadata,
		ThreadID: plan.remoteThreadID,
	}, opts.format)
	if err != nil {
		return "", err
	}

	if len(plan.localRecipients) == 0 {
		return firstRemoteMailboxOutput(remoteOutputs)
	}
	var msg mailbox.Message
	res = ctx.Sess.enqueueCommandMutationContext(ctx.context(), func(mctx *MutationContext) commandMutationResult {
		var err error
		msg, err = mctx.sess.sendMailboxMessage(mailbox.SendRequest{
			Sender:     plan.sender,
			Recipients: plan.localRecipients,
			Topics:     opts.topics,
			Groups:     opts.groups,
			Subject:    opts.subject,
			Body:       opts.body,
			Metadata:   opts.metadata,
			ReplyTo:    opts.replyTo,
		})
		return commandMutationResult{err: err}
	})
	if res.err != nil {
		return "", res.err
	}
	return formatMsgSendOutput(msg, opts.format)
}

func runMsgReplyCommand(ctx *CommandContext, opts msgReplyOptions) (string, error) {
	var plan msgReplyPlan
	res := ctx.Sess.enqueueCommandMutationContext(ctx.context(), func(mctx *MutationContext) commandMutationResult {
		var err error
		plan, err = planMsgReply(mctx, ctx.ActorPaneID, opts)
		return commandMutationResult{err: err}
	})
	if res.err != nil {
		return "", res.err
	}

	remoteOutputs, err := forwardRemoteMailboxDeliveries(ctx, plan.sender, plan.remoteRecipients, msgRemoteDeliverPayload{
		Sender:   plan.sender,
		Topics:   plan.topics,
		Groups:   plan.groups,
		Subject:  opts.subject,
		Body:     opts.body,
		Metadata: opts.metadata,
		ThreadID: plan.remoteThreadID,
	}, opts.format)
	if err != nil {
		return "", err
	}

	var msg mailbox.Message
	if len(plan.localRecipients) > 0 {
		res = ctx.Sess.enqueueCommandMutationContext(ctx.context(), func(mctx *MutationContext) commandMutationResult {
			var err error
			msg, err = mctx.sess.sendMailboxMessage(mailbox.SendRequest{
				Sender:     plan.sender,
				Recipients: plan.localRecipients,
				Topics:     plan.topics,
				Groups:     plan.groups,
				Subject:    opts.subject,
				Body:       opts.body,
				Metadata:   opts.metadata,
				ReplyTo:    plan.parent.ID,
			})
			return commandMutationResult{err: err}
		})
		if res.err != nil {
			return "", res.err
		}
	}

	if opts.ackStatus != "" || opts.ackNote != "" {
		res = ctx.Sess.enqueueCommandMutationContext(ctx.context(), func(mctx *MutationContext) commandMutationResult {
			_, err := mctx.sess.ackMailboxMessage(plan.parent.ID, plan.sender.ID, mailbox.AckRequest{Status: opts.ackStatus, Note: opts.ackNote})
			return commandMutationResult{err: err}
		})
		if res.err != nil {
			return "", res.err
		}
	}

	if msg.ID != "" {
		return formatMsgSendOutput(msg, opts.format)
	}
	return firstRemoteMailboxOutput(remoteOutputs)
}

func runMsgDeliver(mctx *MutationContext, opts msgDeliverOptions) (string, error) {
	recipients, err := resolveMailboxRecipients(mctx, 0, opts.payload.Recipients)
	if err != nil {
		return "", err
	}
	msg, err := mctx.sess.sendMailboxMessage(mailbox.SendRequest{
		Sender:     opts.payload.Sender,
		Recipients: recipients,
		Topics:     opts.payload.Topics,
		Groups:     opts.payload.Groups,
		Subject:    opts.payload.Subject,
		Body:       opts.payload.Body,
		Metadata:   opts.payload.Metadata,
		ReplyTo:    opts.payload.ReplyTo,
		ThreadID:   opts.payload.ThreadID,
	})
	if err != nil {
		return "", err
	}
	return formatMsgSendOutput(msg, opts.format)
}

func planMsgSend(mctx *MutationContext, actorPaneID uint32, opts msgSendOptions) (msgSendPlan, error) {
	sender, err := resolveMailboxSender(mctx, actorPaneID, opts.from)
	if err != nil {
		return msgSendPlan{}, err
	}
	targets, err := resolveMailboxRecipientTargets(mctx, actorPaneID, opts.to)
	if err != nil {
		return msgSendPlan{}, err
	}
	remoteThreadID, err := mailboxThreadForReplyTo(mctx, opts.replyTo)
	if err != nil {
		return msgSendPlan{}, err
	}
	localRecipients, remoteRecipients, err := splitMailboxRecipientTargets(targets)
	if err != nil {
		return msgSendPlan{}, err
	}
	return msgSendPlan{
		sender:           sender,
		localRecipients:  localRecipients,
		remoteRecipients: remoteRecipients,
		remoteThreadID:   remoteThreadID,
	}, nil
}

func planMsgReply(mctx *MutationContext, actorPaneID uint32, opts msgReplyOptions) (msgReplyPlan, error) {
	sender, err := resolveMailboxSender(mctx, actorPaneID, opts.from)
	if err != nil {
		return msgReplyPlan{}, err
	}
	parent, ok := mctx.sess.ensureMailbox().Message(opts.id)
	if !ok {
		return msgReplyPlan{}, fmt.Errorf("message %q not found", opts.id)
	}
	targets, err := resolveMailboxReplyRecipientTargets(mctx, actorPaneID, sender, parent, opts.to)
	if err != nil {
		return msgReplyPlan{}, err
	}
	if opts.ackStatus != "" || opts.ackNote != "" {
		if _, err := mctx.sess.ensureMailbox().DeliverySummary(parent.ID, sender.ID); err != nil {
			return msgReplyPlan{}, fmt.Errorf("cannot ack reply parent as %s: %w", sender.Name, err)
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
	localRecipients, remoteRecipients, err := splitMailboxRecipientTargets(targets)
	if err != nil {
		return msgReplyPlan{}, err
	}
	return msgReplyPlan{
		sender:           sender,
		parent:           parent,
		localRecipients:  localRecipients,
		remoteRecipients: remoteRecipients,
		topics:           topics,
		groups:           groups,
		remoteThreadID:   parent.ThreadID,
	}, nil
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
	return formatMsgReadText(msg, body), nil
}

func runMsgThread(mctx *MutationContext, opts msgThreadOptions) (string, error) {
	messages, err := mctx.sess.ensureMailbox().Thread(opts.ref)
	if err != nil {
		return "", err
	}
	if opts.format == msgFormatJSON {
		return encodeMsgJSON(threadOutput(messages))
	}
	return formatMsgThreadText(messages), nil
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

func resolveMailboxReplyRecipientTargets(mctx *MutationContext, actorPaneID uint32, sender mailbox.PaneAddress, parent mailbox.Message, refs []string) ([]msgRecipientTarget, error) {
	if len(refs) > 0 {
		return resolveMailboxRecipientTargets(mctx, actorPaneID, refs)
	}
	recipients, err := resolveMailboxReplyRecipients(mctx, actorPaneID, sender, parent, nil)
	if err != nil {
		return nil, err
	}
	return mailboxRecipientTargetsForAddresses(mctx, recipients)
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

func resolveMailboxRecipientTargets(mctx *MutationContext, actorPaneID uint32, refs []string) ([]msgRecipientTarget, error) {
	if len(refs) == 0 {
		return nil, fmt.Errorf("at least one recipient is required")
	}
	targets := make([]msgRecipientTarget, 0, len(refs))
	for _, ref := range refs {
		target, err := resolveMailboxPaneTarget(mctx, actorPaneID, ref, "recipient")
		if err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	return targets, nil
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

func resolveMailboxPaneTarget(mctx *MutationContext, actorPaneID uint32, ref, role string) (msgRecipientTarget, error) {
	pane, window, err := mctx.resolvePaneAcrossWindowsForActor(actorPaneID, ref)
	if err != nil {
		return msgRecipientTarget{}, err
	}
	if window == nil {
		return msgRecipientTarget{}, fmt.Errorf("%s pane %q is not in any window", role, ref)
	}
	return mailboxRecipientTargetForPane(mctx, pane, role)
}

func mailboxRecipientTargetsForAddresses(mctx *MutationContext, addrs []mailbox.PaneAddress) ([]msgRecipientTarget, error) {
	targets := make([]msgRecipientTarget, 0, len(addrs))
	for _, addr := range addrs {
		target := msgRecipientTarget{address: addr}
		if isRemoteMailboxAddress(addr) {
			ref, err := mailboxRemoteRefForPaneID(mctx, addr.ID, "recipient")
			if err != nil {
				return nil, err
			}
			target.remoteRef = ref
		}
		targets = append(targets, target)
	}
	return targets, nil
}

func mailboxRecipientTargetForPane(mctx *MutationContext, pane *mux.Pane, role string) (msgRecipientTarget, error) {
	addr := mailboxCommandAddressForPane(pane)
	target := msgRecipientTarget{address: addr}
	if isRemoteMailboxAddress(addr) {
		ref, err := mailboxRemoteRefForPaneID(mctx, addr.ID, role)
		if err != nil {
			return msgRecipientTarget{}, err
		}
		target.remoteRef = ref
	}
	return target, nil
}

func mailboxRemoteRefForPaneID(mctx *MutationContext, paneID uint32, role string) (*checkpoint.RemoteRef, error) {
	if mctx == nil || mctx.sess == nil || mctx.sess.mirror == nil {
		return nil, fmt.Errorf("%s pane %d is remote but mirror manager is not configured", role, paneID)
	}
	ref, ok := mctx.sess.mirror.RemoteRef(paneID)
	if !ok || ref == nil {
		return nil, fmt.Errorf("%s pane %d is remote but has no remote ref", role, paneID)
	}
	return ref, nil
}

func mailboxCommandAddressForPane(pane *mux.Pane) mailbox.PaneAddress {
	if pane == nil {
		return mailbox.PaneAddress{}
	}
	return mailbox.PaneAddress{ID: pane.ID, Name: pane.Meta.Name, Host: pane.Meta.Host}
}

func splitMailboxRecipientTargets(targets []msgRecipientTarget) ([]mailbox.PaneAddress, map[string][]msgRemoteRecipient, error) {
	localRecipients := make([]mailbox.PaneAddress, 0, len(targets))
	remoteRecipients := make(map[string][]msgRemoteRecipient)
	for _, target := range targets {
		if !isRemoteMailboxAddress(target.address) {
			localRecipients = append(localRecipients, target.address)
			continue
		}
		if target.remoteRef == nil {
			return nil, nil, fmt.Errorf("recipient pane %d is remote but has no remote ref", target.address.ID)
		}
		remoteRecipients[target.remoteRef.Host] = append(remoteRecipients[target.remoteRef.Host], msgRemoteRecipient{
			ref: *target.remoteRef,
		})
	}
	return localRecipients, remoteRecipients, nil
}

func isRemoteMailboxAddress(addr mailbox.PaneAddress) bool {
	host := strings.TrimSpace(addr.Host)
	return host != "" && host != mux.DefaultHost
}

func mailboxThreadForReplyTo(mctx *MutationContext, replyTo mailbox.MessageID) (mailbox.ThreadID, error) {
	if replyTo == "" {
		return "", nil
	}
	parent, ok := mctx.sess.ensureMailbox().Message(replyTo)
	if !ok {
		return "", fmt.Errorf("reply parent %q not found", replyTo)
	}
	return parent.ThreadID, nil
}

func forwardRemoteMailboxDeliveries(ctx *CommandContext, sender mailbox.PaneAddress, recipients map[string][]msgRemoteRecipient, base msgRemoteDeliverPayload, format msgFormat) ([]string, error) {
	if len(recipients) == 0 {
		return nil, nil
	}
	outputs := make([]string, 0, len(recipients))
	hostNames := make([]string, 0, len(recipients))
	for hostName := range recipients {
		hostNames = append(hostNames, hostName)
	}
	sort.Strings(hostNames)
	for _, hostName := range hostNames {
		hostRecipients := recipients[hostName]
		if len(hostRecipients) == 0 {
			continue
		}
		payload := base
		payload.Sender = sender
		payload.Recipients = remoteMailboxRecipientRefs(hostRecipients)
		output, err := forwardRemoteMailboxDelivery(ctx, hostRecipients[0].ref, payload, format)
		if err != nil {
			return nil, err
		}
		if output == "" {
			output = fmt.Sprintf("Sent mailbox message to %s\n", hostName)
		}
		outputs = append(outputs, output)
	}
	return outputs, nil
}

func remoteMailboxRecipientRefs(recipients []msgRemoteRecipient) []string {
	refs := make([]string, 0, len(recipients))
	for _, recipient := range recipients {
		refs = append(refs, recipient.ref.PaneName)
	}
	return refs
}

func forwardRemoteMailboxDelivery(ctx *CommandContext, ref checkpoint.RemoteRef, payload msgRemoteDeliverPayload, format msgFormat) (string, error) {
	host, dialer, err := mailboxRemoteTransport(ctx, ref)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	args := []string{"deliver", string(raw)}
	if format == msgFormatJSON {
		args = append(args, "--format", "json")
	}
	msg, err := runRemoteOneShotCommandWithDialer(ctx.context(), host, dialer, "msg", args)
	if err != nil {
		return "", fmt.Errorf("forward mailbox message to %s: %w", ref.Host, err)
	}
	return msg.CmdOutput, nil
}

func mailboxRemoteTransport(ctx *CommandContext, ref checkpoint.RemoteRef) (config.Host, remote.Dialer, error) {
	if ref.Host == "" {
		return config.Host{}, nil, fmt.Errorf("remote host is required")
	}
	var (
		host   config.Host
		dialer remote.Dialer
		ok     bool
	)
	if ctx != nil && ctx.Sess != nil && ctx.Sess.mirror != nil {
		host, ok = ctx.Sess.mirror.Host(ref.Host)
		dialer = ctx.Sess.mirror.Dialer()
	}
	if !ok {
		var err error
		host, err = lookupRemoteHost(ref.Host)
		if err != nil {
			return config.Host{}, nil, err
		}
	}
	if strings.TrimSpace(host.Session) == "" {
		host.Session = ref.Session
	}
	if strings.TrimSpace(host.Session) == "" {
		host.Session = DefaultSessionName
	}
	return host, dialer, nil
}

func firstRemoteMailboxOutput(outputs []string) (string, error) {
	if len(outputs) == 0 {
		return "", fmt.Errorf("at least one recipient is required")
	}
	return outputs[0], nil
}

func formatMsgSendOutput(msg mailbox.Message, format msgFormat) (string, error) {
	if format == msgFormatJSON {
		return encodeMsgJSON(sendOutputForMessage(msg))
	}
	return fmt.Sprintf("Sent %s to %s\n", msg.ID, joinPaneNames(msg.Recipients)), nil
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
		PendingIDs:         cloneMsgIDs(status.PendingIDs),
		Latest:             drainStatusLatestOutput(status.Latest),
	}
}

func cloneMsgIDs(ids []mailbox.MessageID) []mailbox.MessageID {
	out := make([]mailbox.MessageID, len(ids))
	copy(out, ids)
	return out
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
		From:        summary.Sender,
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
		return "..."[:limit]
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
		From:       msg.Sender,
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

func threadOutput(messages []mailbox.Message) []msgThreadMessageOutput {
	out := make([]msgThreadMessageOutput, 0, len(messages))
	for _, msg := range messages {
		out = append(out, msgThreadMessageOutput{
			ID:         msg.ID,
			Sender:     msg.Sender,
			Recipients: append([]mailbox.PaneAddress(nil), msg.Recipients...),
			Subject:    msg.Subject,
			Topics:     append([]string(nil), msg.Topics...),
			Groups:     append([]string(nil), msg.Groups...),
			ThreadID:   msg.ThreadID,
			InReplyTo:  msg.InReplyTo,
			CreatedAt:  msg.CreatedAt.Format(time.RFC3339Nano),
			Body:       msgBodyText(msg),
			BodySize:   messageOutputBodySize(msg),
			PartCount:  len(msg.Parts),
		})
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

func formatMsgReadText(msg mailbox.Message, body string) string {
	return fmt.Sprintf("From: %s (%d)\n\n%s", msg.Sender.Name, msg.Sender.ID, ensureMsgTrailingNewline(body))
}

func formatMsgInboxText(summaries []mailbox.DeliverySummary) string {
	if len(summaries) == 0 {
		return ""
	}
	var b strings.Builder
	for _, summary := range summaries {
		fmt.Fprintf(&b, "%s from %s (%d): %s (%d bytes)\n", summary.MessageID, summary.Sender.Name, summary.Sender.ID, summary.Subject, summary.BodySize)
	}
	return b.String()
}

func formatMsgThreadText(messages []mailbox.Message) string {
	if len(messages) == 0 {
		return ""
	}
	var b strings.Builder
	for i, msg := range messages {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%s from %s at %s", msg.ID, msg.Sender.Name, msg.CreatedAt.Format(time.RFC3339Nano))
		if msg.Subject != "" {
			fmt.Fprintf(&b, ": %s", msg.Subject)
		}
		b.WriteByte('\n')
		b.WriteString(ensureMsgTrailingNewline(msgBodyText(msg)))
	}
	return b.String()
}
