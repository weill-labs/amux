package mailbox

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	DefaultContentType = "text/plain; charset=utf-8"
	EncodingUTF8       = "utf-8"
	EncodingBase64     = "base64"
)

const (
	defaultSubjectBytes       = 512
	defaultPartBytes          = 64 * 1024
	defaultTotalBodyBytes     = 256 * 1024
	defaultMetadataBytes      = 16 * 1024
	defaultAckNoteBytes       = 4 * 1024
	defaultRecipientsPerMsg   = 128
	defaultStoredMessages     = 10_000
	defaultStoredMailboxBytes = 64 * 1024 * 1024
)

var labelRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,63}$`)

type MessageID string

type ThreadID string

type PaneAddress struct {
	ID   uint32 `json:"id"`
	Name string `json:"name"`
	Host string `json:"host,omitempty"`
}

type MessagePart struct {
	Name        string `json:"name,omitempty"`
	ContentType string `json:"content_type"`
	Encoding    string `json:"encoding"`
	Bytes       []byte `json:"bytes"`
	Size        int    `json:"size"`
}

type Message struct {
	ID         MessageID                  `json:"id"`
	ThreadID   ThreadID                   `json:"thread_id"`
	InReplyTo  MessageID                  `json:"in_reply_to,omitempty"`
	Replies    []MessageID                `json:"replies,omitempty"`
	Sender     PaneAddress                `json:"sender"`
	Recipients []PaneAddress              `json:"recipients"`
	Topics     []string                   `json:"topics,omitempty"`
	Groups     []string                   `json:"groups,omitempty"`
	Subject    string                     `json:"subject"`
	Parts      []MessagePart              `json:"parts"`
	Metadata   map[string]json.RawMessage `json:"metadata,omitempty"`
	CreatedAt  time.Time                  `json:"created_at"`
	UpdatedAt  time.Time                  `json:"updated_at"`
	ExpiresAt  time.Time                  `json:"expires_at,omitempty"`
}

type DeliveryState struct {
	MessageID    MessageID   `json:"message_id"`
	Recipient    PaneAddress `json:"recipient"`
	DeliveredAt  time.Time   `json:"delivered_at"`
	ReadAt       time.Time   `json:"read_at,omitempty"`
	AckedAt      time.Time   `json:"acked_at,omitempty"`
	AckStatus    string      `json:"ack_status,omitempty"`
	AckNote      string      `json:"ack_note,omitempty"`
	LastEventSeq uint64      `json:"last_event_seq,omitempty"`
}

type DeliverySummary struct {
	MessageID    MessageID   `json:"message_id"`
	Sender       PaneAddress `json:"sender"`
	Recipient    PaneAddress `json:"recipient"`
	Subject      string      `json:"subject"`
	Topics       []string    `json:"topics,omitempty"`
	Groups       []string    `json:"groups,omitempty"`
	ThreadID     ThreadID    `json:"thread_id"`
	InReplyTo    MessageID   `json:"in_reply_to,omitempty"`
	CreatedAt    time.Time   `json:"created_at"`
	DeliveredAt  time.Time   `json:"delivered_at"`
	ReadAt       time.Time   `json:"read_at,omitempty"`
	AckedAt      time.Time   `json:"acked_at,omitempty"`
	AckStatus    string      `json:"ack_status,omitempty"`
	AckNote      string      `json:"ack_note,omitempty"`
	LastEventSeq uint64      `json:"last_event_seq,omitempty"`
	BodySize     int         `json:"body_size"`
	PartCount    int         `json:"part_count"`
}

type SendRequest struct {
	Sender     PaneAddress
	Recipients []PaneAddress
	Topics     []string
	Groups     []string
	Subject    string
	Body       []byte
	Parts      []MessagePart
	Metadata   map[string]json.RawMessage
	ReplyTo    MessageID
	ThreadID   ThreadID
	ExpiresAt  time.Time
}

type ReadOptions struct {
	Peek bool
}

type ListOptions struct {
	UnreadOnly bool
}

type AckRequest struct {
	Status string
	Note   string
}

type Options struct {
	Now    func() time.Time
	Limits Limits
}

type Limits struct {
	SubjectBytes       int
	PartBytes          int
	TotalBodyBytes     int
	MetadataBytes      int
	AckNoteBytes       int
	RecipientsPerMsg   int
	StoredMessages     int
	StoredMailboxBytes int
}

func DefaultLimits() Limits {
	return Limits{
		SubjectBytes:       defaultSubjectBytes,
		PartBytes:          defaultPartBytes,
		TotalBodyBytes:     defaultTotalBodyBytes,
		MetadataBytes:      defaultMetadataBytes,
		AckNoteBytes:       defaultAckNoteBytes,
		RecipientsPerMsg:   defaultRecipientsPerMsg,
		StoredMessages:     defaultStoredMessages,
		StoredMailboxBytes: defaultStoredMailboxBytes,
	}
}

type Store struct {
	nextSeq    uint64
	messages   map[MessageID]*Message
	deliveries map[uint32]map[MessageID]*DeliveryState
	threads    map[ThreadID][]MessageID
	now        func() time.Time
	limits     Limits
}

type Snapshot struct {
	NextSeq    uint64          `json:"next_seq,omitempty"`
	Messages   []Message       `json:"messages,omitempty"`
	Deliveries []DeliveryState `json:"deliveries,omitempty"`
}

func NewStore(opts Options) *Store {
	limits := normalizeLimits(opts.Limits)
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Store{
		nextSeq:    1,
		messages:   make(map[MessageID]*Message),
		deliveries: make(map[uint32]map[MessageID]*DeliveryState),
		threads:    make(map[ThreadID][]MessageID),
		now:        now,
		limits:     limits,
	}
}

func (s *Store) Len() int {
	if s == nil {
		return 0
	}
	return len(s.messages)
}

func (s *Store) Snapshot() Snapshot {
	if s == nil {
		return Snapshot{}
	}

	ids := make([]MessageID, 0, len(s.messages))
	for id := range s.messages {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		return ids[i] < ids[j]
	})

	messages := make([]Message, 0, len(ids))
	for _, id := range ids {
		messages = append(messages, cloneMessage(s.messages[id]))
	}

	recipientIDs := make([]uint32, 0, len(s.deliveries))
	for recipientID := range s.deliveries {
		recipientIDs = append(recipientIDs, recipientID)
	}
	sort.Slice(recipientIDs, func(i, j int) bool {
		return recipientIDs[i] < recipientIDs[j]
	})

	var deliveries []DeliveryState
	for _, recipientID := range recipientIDs {
		byMessage := s.deliveries[recipientID]
		messageIDs := make([]MessageID, 0, len(byMessage))
		for id := range byMessage {
			messageIDs = append(messageIDs, id)
		}
		sort.Slice(messageIDs, func(i, j int) bool {
			return messageIDs[i] < messageIDs[j]
		})
		for _, id := range messageIDs {
			deliveries = append(deliveries, *byMessage[id])
		}
	}

	return Snapshot{
		NextSeq:    s.nextSeq,
		Messages:   messages,
		Deliveries: deliveries,
	}
}

func RestoreSnapshot(snapshot Snapshot, opts Options) (*Store, error) {
	store := NewStore(opts)
	if snapshot.NextSeq != 0 {
		store.nextSeq = snapshot.NextSeq
	}

	var maxSeq uint64
	for _, src := range snapshot.Messages {
		msg := cloneMessage(&src)
		if msg.ID == "" {
			return nil, fmt.Errorf("mailbox snapshot message ID is required")
		}
		if _, exists := store.messages[msg.ID]; exists {
			return nil, fmt.Errorf("duplicate mailbox snapshot message %q", msg.ID)
		}
		if seq, ok := parseMessageSeq(msg.ID); ok && seq > maxSeq {
			maxSeq = seq
		}
		if msg.ThreadID != "" {
			store.threads[msg.ThreadID] = append(store.threads[msg.ThreadID], msg.ID)
		}
		store.messages[msg.ID] = &msg
	}

	for _, src := range snapshot.Deliveries {
		delivery := src
		if delivery.MessageID == "" {
			return nil, fmt.Errorf("mailbox snapshot delivery message ID is required")
		}
		if delivery.Recipient.ID == 0 {
			return nil, fmt.Errorf("mailbox snapshot delivery recipient pane ID is required")
		}
		if store.messages[delivery.MessageID] == nil {
			return nil, fmt.Errorf("mailbox snapshot delivery references missing message %q", delivery.MessageID)
		}
		byMessage := store.deliveries[delivery.Recipient.ID]
		if byMessage == nil {
			byMessage = make(map[MessageID]*DeliveryState)
			store.deliveries[delivery.Recipient.ID] = byMessage
		}
		if _, exists := byMessage[delivery.MessageID]; exists {
			return nil, fmt.Errorf("duplicate mailbox snapshot delivery for message %q recipient pane %d", delivery.MessageID, delivery.Recipient.ID)
		}
		byMessage[delivery.MessageID] = &delivery
	}

	if store.nextSeq == 0 {
		store.nextSeq = 1
	}
	if maxSeq >= store.nextSeq {
		store.nextSeq = maxSeq + 1
	}

	return store, nil
}

func (s *Store) MaxLastEventSeq() uint64 {
	if s == nil {
		return 0
	}
	var maxSeq uint64
	for _, byMessage := range s.deliveries {
		for _, delivery := range byMessage {
			if delivery.LastEventSeq > maxSeq {
				maxSeq = delivery.LastEventSeq
			}
		}
	}
	return maxSeq
}

func (s *Store) Send(req SendRequest) (Message, error) {
	if s == nil {
		return Message{}, fmt.Errorf("mailbox store is nil")
	}
	if err := s.validateCanStoreMessage(); err != nil {
		return Message{}, err
	}
	sender, err := validateAddress("sender", req.Sender)
	if err != nil {
		return Message{}, err
	}
	recipients, err := validateRecipients(req.Recipients, s.limits.RecipientsPerMsg)
	if err != nil {
		return Message{}, err
	}
	if err := validateSubject(req.Subject, s.limits.SubjectBytes); err != nil {
		return Message{}, err
	}
	topics, err := validateLabels("topic", req.Topics)
	if err != nil {
		return Message{}, err
	}
	groups, err := validateLabels("group", req.Groups)
	if err != nil {
		return Message{}, err
	}
	parts, bodySize, err := normalizeParts(req.Body, req.Parts, s.limits.PartBytes, s.limits.TotalBodyBytes)
	if err != nil {
		return Message{}, err
	}
	metadata, err := validateMetadata(req.Metadata, s.limits.MetadataBytes)
	if err != nil {
		return Message{}, err
	}
	parent, threadID, err := s.resolveThread(req.ReplyTo, req.ThreadID)
	if err != nil {
		return Message{}, err
	}
	if err := s.validateMailboxByteLimit(bodySize, metadataByteSize(metadata)); err != nil {
		return Message{}, err
	}

	now := s.clock()
	id := s.allocateID()
	if threadID == "" {
		threadID = ThreadID(id)
	}
	msg := &Message{
		ID:         id,
		ThreadID:   threadID,
		InReplyTo:  req.ReplyTo,
		Sender:     sender,
		Recipients: recipients,
		Topics:     topics,
		Groups:     groups,
		Subject:    req.Subject,
		Parts:      parts,
		Metadata:   metadata,
		CreatedAt:  now,
		UpdatedAt:  now,
		ExpiresAt:  req.ExpiresAt.UTC(),
	}
	s.messages[id] = msg
	s.threads[threadID] = append(s.threads[threadID], id)
	if parent != nil {
		parent.Replies = append(parent.Replies, id)
		parent.UpdatedAt = now
	}
	for _, recipient := range recipients {
		byMessage := s.deliveries[recipient.ID]
		if byMessage == nil {
			byMessage = make(map[MessageID]*DeliveryState)
			s.deliveries[recipient.ID] = byMessage
		}
		byMessage[id] = &DeliveryState{
			MessageID:   id,
			Recipient:   recipient,
			DeliveredAt: now,
		}
	}

	return cloneMessage(msg), nil
}

func (s *Store) Message(id MessageID) (Message, bool) {
	if s == nil {
		return Message{}, false
	}
	msg, ok := s.messages[id]
	if !ok {
		return Message{}, false
	}
	return cloneMessage(msg), true
}

func (s *Store) ListUnread(recipientID uint32) ([]DeliverySummary, error) {
	return s.List(recipientID, ListOptions{UnreadOnly: true})
}

func (s *Store) List(recipientID uint32, opts ListOptions) ([]DeliverySummary, error) {
	if s == nil {
		return nil, fmt.Errorf("mailbox store is nil")
	}
	if recipientID == 0 {
		return nil, fmt.Errorf("recipient pane ID is required")
	}
	byMessage := s.deliveries[recipientID]
	if len(byMessage) == 0 {
		return nil, nil
	}
	ids := make([]MessageID, 0, len(byMessage))
	for id, delivery := range byMessage {
		if opts.UnreadOnly && (!delivery.ReadAt.IsZero() || !delivery.AckedAt.IsZero()) {
			continue
		}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		return ids[i] < ids[j]
	})
	summaries := make([]DeliverySummary, 0, len(ids))
	for _, id := range ids {
		summaries = append(summaries, summaryFor(s.messages[id], byMessage[id]))
	}
	return summaries, nil
}

func (s *Store) DeliverySummary(id MessageID, recipientID uint32) (DeliverySummary, error) {
	if s == nil {
		return DeliverySummary{}, fmt.Errorf("mailbox store is nil")
	}
	msg, delivery, err := s.messageDelivery(id, recipientID)
	if err != nil {
		return DeliverySummary{}, err
	}
	return summaryFor(msg, delivery), nil
}

func (s *Store) SetLastEventSeq(id MessageID, recipientID uint32, seq uint64) (DeliverySummary, error) {
	if s == nil {
		return DeliverySummary{}, fmt.Errorf("mailbox store is nil")
	}
	msg, delivery, err := s.messageDelivery(id, recipientID)
	if err != nil {
		return DeliverySummary{}, err
	}
	delivery.LastEventSeq = seq
	return summaryFor(msg, delivery), nil
}

func (s *Store) Read(id MessageID, recipientID uint32, opts ReadOptions) (Message, DeliveryState, error) {
	if s == nil {
		return Message{}, DeliveryState{}, fmt.Errorf("mailbox store is nil")
	}
	msg, delivery, err := s.messageDelivery(id, recipientID)
	if err != nil {
		return Message{}, DeliveryState{}, err
	}
	if !opts.Peek && delivery.ReadAt.IsZero() {
		now := s.clock()
		delivery.ReadAt = now
		msg.UpdatedAt = now
	}
	return cloneMessage(msg), *delivery, nil
}

func (s *Store) Ack(id MessageID, recipientID uint32, req AckRequest) (DeliveryState, error) {
	if s == nil {
		return DeliveryState{}, fmt.Errorf("mailbox store is nil")
	}
	if len([]byte(req.Note)) > s.limits.AckNoteBytes {
		return DeliveryState{}, fmt.Errorf("ack note exceeds %d bytes", s.limits.AckNoteBytes)
	}
	msg, delivery, err := s.messageDelivery(id, recipientID)
	if err != nil {
		return DeliveryState{}, err
	}
	if delivery.AckedAt.IsZero() || delivery.AckStatus != req.Status || delivery.AckNote != req.Note {
		now := s.clock()
		delivery.AckedAt = now
		delivery.AckStatus = req.Status
		delivery.AckNote = req.Note
		msg.UpdatedAt = now
	}
	return *delivery, nil
}

func (s *Store) validateCanStoreMessage() error {
	if s.limits.StoredMessages > 0 && len(s.messages) >= s.limits.StoredMessages {
		return fmt.Errorf("mailbox stored message limit reached (%d)", s.limits.StoredMessages)
	}
	return nil
}

func (s *Store) validateMailboxByteLimit(bodyBytes, metadataBytes int) error {
	if s.limits.StoredMailboxBytes <= 0 {
		return nil
	}
	total := bodyBytes + metadataBytes
	for _, msg := range s.messages {
		total += messageBodySize(msg) + metadataByteSize(msg.Metadata)
	}
	if total > s.limits.StoredMailboxBytes {
		return fmt.Errorf("mailbox byte limit exceeded (%d > %d)", total, s.limits.StoredMailboxBytes)
	}
	return nil
}

func (s *Store) resolveThread(replyTo MessageID, requested ThreadID) (*Message, ThreadID, error) {
	if replyTo == "" {
		return nil, requested, nil
	}
	parent := s.messages[replyTo]
	if parent == nil {
		return nil, "", fmt.Errorf("reply parent %q not found", replyTo)
	}
	if requested != "" && requested != parent.ThreadID {
		return nil, "", fmt.Errorf("reply thread %q does not match parent thread %q", requested, parent.ThreadID)
	}
	return parent, parent.ThreadID, nil
}

func (s *Store) allocateID() MessageID {
	id := MessageID(fmt.Sprintf("msg-%06d", s.nextSeq))
	s.nextSeq++
	return id
}

func parseMessageSeq(id MessageID) (uint64, bool) {
	raw := string(id)
	if !strings.HasPrefix(raw, "msg-") {
		return 0, false
	}
	seq, err := strconv.ParseUint(strings.TrimPrefix(raw, "msg-"), 10, 64)
	if err != nil {
		return 0, false
	}
	return seq, true
}

func (s *Store) messageDelivery(id MessageID, recipientID uint32) (*Message, *DeliveryState, error) {
	if recipientID == 0 {
		return nil, nil, fmt.Errorf("recipient pane ID is required")
	}
	msg := s.messages[id]
	if msg == nil {
		return nil, nil, fmt.Errorf("message %q not found", id)
	}
	delivery := s.deliveries[recipientID][id]
	if delivery == nil {
		return nil, nil, fmt.Errorf("message %q not delivered to recipient pane %d", id, recipientID)
	}
	return msg, delivery, nil
}

func (s *Store) clock() time.Time {
	return s.now().UTC()
}

func validateAddress(role string, addr PaneAddress) (PaneAddress, error) {
	if addr.ID == 0 {
		return PaneAddress{}, fmt.Errorf("%s pane ID is required", role)
	}
	if strings.TrimSpace(addr.Name) == "" {
		return PaneAddress{}, fmt.Errorf("%s pane name is required", role)
	}
	return addr, nil
}

func validateRecipients(src []PaneAddress, limit int) ([]PaneAddress, error) {
	if len(src) == 0 {
		return nil, fmt.Errorf("at least one recipient is required")
	}
	if limit > 0 && len(src) > limit {
		return nil, fmt.Errorf("recipient count %d exceeds limit %d", len(src), limit)
	}
	recipients := make([]PaneAddress, 0, len(src))
	seen := make(map[uint32]struct{}, len(src))
	for _, addr := range src {
		recipient, err := validateAddress("recipient", addr)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[recipient.ID]; ok {
			return nil, fmt.Errorf("duplicate recipient pane %d", recipient.ID)
		}
		seen[recipient.ID] = struct{}{}
		recipients = append(recipients, recipient)
	}
	return recipients, nil
}

func validateSubject(subject string, limit int) error {
	if len([]byte(subject)) > limit {
		return fmt.Errorf("subject exceeds %d bytes", limit)
	}
	if !utf8.ValidString(subject) {
		return fmt.Errorf("subject must be valid UTF-8")
	}
	for _, r := range subject {
		if unicode.IsControl(r) {
			return fmt.Errorf("subject contains control character %q", r)
		}
	}
	return nil
}

func validateLabels(kind string, src []string) ([]string, error) {
	if len(src) == 0 {
		return nil, nil
	}
	out := make([]string, len(src))
	seen := make(map[string]struct{}, len(src))
	for i, label := range src {
		if !labelRE.MatchString(label) {
			return nil, fmt.Errorf("invalid %s %q", kind, label)
		}
		if _, ok := seen[label]; ok {
			return nil, fmt.Errorf("duplicate %s %q", kind, label)
		}
		seen[label] = struct{}{}
		out[i] = label
	}
	return out, nil
}

func normalizeParts(body []byte, parts []MessagePart, partLimit, totalLimit int) ([]MessagePart, int, error) {
	if len(body) != 0 && len(parts) != 0 {
		return nil, 0, fmt.Errorf("body and message parts are mutually exclusive")
	}
	if len(body) != 0 {
		parts = []MessagePart{{
			Name:        "body",
			ContentType: DefaultContentType,
			Encoding:    EncodingUTF8,
			Bytes:       body,
		}}
	}
	if len(parts) == 0 {
		return nil, 0, fmt.Errorf("message body is required")
	}
	out := make([]MessagePart, len(parts))
	total := 0
	for i, part := range parts {
		normalized, err := normalizePart(i, part, partLimit)
		if err != nil {
			return nil, 0, err
		}
		total += normalized.Size
		if totalLimit > 0 && total > totalLimit {
			return nil, 0, fmt.Errorf("message body exceeds %d bytes", totalLimit)
		}
		out[i] = normalized
	}
	return out, total, nil
}

func normalizePart(i int, part MessagePart, limit int) (MessagePart, error) {
	if part.ContentType == "" {
		part.ContentType = DefaultContentType
	}
	if part.Encoding == "" {
		part.Encoding = EncodingUTF8
	}
	if part.Name == "" {
		part.Name = fmt.Sprintf("part-%d", i+1)
	}
	part.Bytes = append([]byte(nil), part.Bytes...)
	part.Size = len(part.Bytes)
	if limit > 0 && part.Size > limit {
		return MessagePart{}, fmt.Errorf("message part %q exceeds %d bytes", part.Name, limit)
	}
	if part.Size == 0 {
		return MessagePart{}, fmt.Errorf("message part %q is empty", part.Name)
	}
	switch part.Encoding {
	case EncodingUTF8:
		if !utf8.Valid(part.Bytes) {
			return MessagePart{}, fmt.Errorf("message part %q must be valid UTF-8", part.Name)
		}
		for _, b := range part.Bytes {
			if b < 0x20 && b != '\n' && b != '\t' {
				return MessagePart{}, fmt.Errorf("message part %q contains control byte 0x%02x", part.Name, b)
			}
		}
	case EncodingBase64:
	default:
		return MessagePart{}, fmt.Errorf("message part %q has unsupported encoding %q", part.Name, part.Encoding)
	}
	return part, nil
}

func validateMetadata(src map[string]json.RawMessage, limit int) (map[string]json.RawMessage, error) {
	if len(src) == 0 {
		return nil, nil
	}
	out := make(map[string]json.RawMessage, len(src))
	for key, value := range src {
		if key == "" {
			return nil, fmt.Errorf("metadata key is required")
		}
		if strings.HasPrefix(key, "amux.") {
			return nil, fmt.Errorf("metadata key %q uses reserved amux. prefix", key)
		}
		if !json.Valid(value) {
			return nil, fmt.Errorf("metadata value for %q is invalid JSON", key)
		}
		out[key] = append(json.RawMessage(nil), value...)
	}
	size := metadataByteSize(out)
	if limit > 0 && size > limit {
		return nil, fmt.Errorf("metadata exceeds %d bytes", limit)
	}
	return out, nil
}

func metadataByteSize(metadata map[string]json.RawMessage) int {
	if len(metadata) == 0 {
		return 0
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return 0
	}
	return len(raw)
}

func summaryFor(msg *Message, delivery *DeliveryState) DeliverySummary {
	return DeliverySummary{
		MessageID:    msg.ID,
		Sender:       msg.Sender,
		Recipient:    delivery.Recipient,
		Subject:      msg.Subject,
		Topics:       append([]string(nil), msg.Topics...),
		Groups:       append([]string(nil), msg.Groups...),
		ThreadID:     msg.ThreadID,
		InReplyTo:    msg.InReplyTo,
		CreatedAt:    msg.CreatedAt,
		DeliveredAt:  delivery.DeliveredAt,
		ReadAt:       delivery.ReadAt,
		AckedAt:      delivery.AckedAt,
		AckStatus:    delivery.AckStatus,
		AckNote:      delivery.AckNote,
		LastEventSeq: delivery.LastEventSeq,
		BodySize:     messageBodySize(msg),
		PartCount:    len(msg.Parts),
	}
}

func messageBodySize(msg *Message) int {
	total := 0
	for _, part := range msg.Parts {
		total += part.Size
	}
	return total
}

func cloneMessage(msg *Message) Message {
	if msg == nil {
		return Message{}
	}
	out := *msg
	out.Recipients = append([]PaneAddress(nil), msg.Recipients...)
	out.Topics = append([]string(nil), msg.Topics...)
	out.Groups = append([]string(nil), msg.Groups...)
	out.Replies = append([]MessageID(nil), msg.Replies...)
	out.Parts = cloneParts(msg.Parts)
	out.Metadata = cloneMetadata(msg.Metadata)
	return out
}

func cloneParts(src []MessagePart) []MessagePart {
	if len(src) == 0 {
		return nil
	}
	out := make([]MessagePart, len(src))
	for i, part := range src {
		out[i] = part
		out[i].Bytes = append([]byte(nil), part.Bytes...)
	}
	return out
}

func cloneMetadata(src map[string]json.RawMessage) map[string]json.RawMessage {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]json.RawMessage, len(src))
	for key, value := range src {
		out[key] = append(json.RawMessage(nil), value...)
	}
	return out
}

func normalizeLimits(l Limits) Limits {
	defaults := DefaultLimits()
	if l.SubjectBytes == 0 {
		l.SubjectBytes = defaults.SubjectBytes
	}
	if l.PartBytes == 0 {
		l.PartBytes = defaults.PartBytes
	}
	if l.TotalBodyBytes == 0 {
		l.TotalBodyBytes = defaults.TotalBodyBytes
	}
	if l.MetadataBytes == 0 {
		l.MetadataBytes = defaults.MetadataBytes
	}
	if l.AckNoteBytes == 0 {
		l.AckNoteBytes = defaults.AckNoteBytes
	}
	if l.RecipientsPerMsg == 0 {
		l.RecipientsPerMsg = defaults.RecipientsPerMsg
	}
	if l.StoredMessages == 0 {
		l.StoredMessages = defaults.StoredMessages
	}
	if l.StoredMailboxBytes == 0 {
		l.StoredMailboxBytes = defaults.StoredMailboxBytes
	}
	return l
}
