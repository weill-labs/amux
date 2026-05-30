package proto

// MailboxPaneSummary identifies a pane in mailbox event summaries.
type MailboxPaneSummary struct {
	ID   uint32 `json:"id"`
	Name string `json:"name"`
	Host string `json:"host,omitempty"`
}

// MailboxMessageSummary is the summary-only mailbox payload used by events and waits.
// It intentionally omits message bodies and arbitrary metadata values.
type MailboxMessageSummary struct {
	ID           string             `json:"id"`
	From         MailboxPaneSummary `json:"from"`
	Subject      string             `json:"subject,omitempty"`
	Topics       []string           `json:"topics,omitempty"`
	Groups       []string           `json:"groups,omitempty"`
	ThreadID     string             `json:"thread_id,omitempty"`
	InReplyTo    string             `json:"in_reply_to,omitempty"`
	BodySize     int                `json:"body_size"`
	PartCount    int                `json:"part_count"`
	CreatedAt    string             `json:"created_at,omitempty"`
	DeliveredAt  string             `json:"delivered_at,omitempty"`
	ReadAt       string             `json:"read_at,omitempty"`
	AckedAt      string             `json:"acked_at,omitempty"`
	AckStatus    string             `json:"ack_status,omitempty"`
	LastEventSeq uint64             `json:"last_event_seq,omitempty"`
}
