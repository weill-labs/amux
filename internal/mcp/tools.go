package mcp

func MailboxTools() []Tool {
	return []Tool{
		{
			Name:        "amux_mailbox_send",
			Title:       "Send Mailbox Message",
			Description: "Send a message from one amux pane mailbox address to one or more recipient panes.",
			InputSchema: objectSchema(map[string]any{
				"from":     stringProperty("Sender pane name or numeric ID."),
				"to":       arrayProperty("Recipient pane names or numeric IDs.", stringProperty("Recipient pane reference.")),
				"subject":  stringProperty("Optional message subject."),
				"body":     stringProperty("Message body text."),
				"topics":   arrayProperty("Optional topic labels.", stringProperty("Topic label.")),
				"groups":   arrayProperty("Optional group labels.", stringProperty("Group label.")),
				"metadata": objectProperty("Optional JSON metadata object."),
				"reply_to": stringProperty("Optional message ID this message replies to."),
			}, []string{"from", "to", "body"}),
			OutputSchema: objectProperty("Sent message summary."),
		},
		{
			Name:        "amux_mailbox_inbox",
			Title:       "List Mailbox Inbox",
			Description: "List mailbox message summaries delivered to an amux pane.",
			InputSchema: objectSchema(map[string]any{
				"pane":   stringProperty("Recipient pane name or numeric ID."),
				"unread": boolProperty("When true, include only unread and unacked messages."),
			}, []string{"pane"}),
			OutputSchema: objectSchema(map[string]any{
				"messages": arrayProperty("Message summaries.", objectProperty("Mailbox message summary.")),
			}, []string{"messages"}),
		},
		{
			Name:        "amux_mailbox_read",
			Title:       "Read Mailbox Message",
			Description: "Read a mailbox message body for a recipient pane.",
			InputSchema: objectSchema(map[string]any{
				"id":   stringProperty("Message ID, for example msg-000001."),
				"pane": stringProperty("Recipient pane name or numeric ID."),
				"peek": boolProperty("When true, return the body without marking the delivery read."),
			}, []string{"id", "pane"}),
			OutputSchema: objectProperty("Read message payload."),
		},
		{
			Name:        "amux_mailbox_ack",
			Title:       "Acknowledge Mailbox Message",
			Description: "Acknowledge a mailbox delivery for a recipient pane.",
			InputSchema: objectSchema(map[string]any{
				"id":     stringProperty("Message ID, for example msg-000001."),
				"pane":   stringProperty("Recipient pane name or numeric ID."),
				"status": stringProperty("Optional acknowledgement status such as ok, error, or seen."),
				"note":   stringProperty("Optional acknowledgement note."),
			}, []string{"id", "pane"}),
			OutputSchema: objectProperty("Acknowledgement delivery state."),
		},
		{
			Name:        "amux_mailbox_watch",
			Title:       "Watch Mailbox Message",
			Description: "Wait for an unread or newly delivered mailbox message summary for a pane.",
			InputSchema: objectSchema(map[string]any{
				"pane":       stringProperty("Recipient pane name or numeric ID."),
				"topic":      stringProperty("Optional topic label filter."),
				"after":      stringProperty("Optional message ID or event sequence to wait after."),
				"timeout_ms": integerProperty("Optional timeout in milliseconds. The amux default is 5000 ms."),
			}, []string{"pane"}),
			OutputSchema: objectProperty("Watched message summary."),
		},
	}
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = append([]string(nil), required...)
	}
	return schema
}

func stringProperty(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func boolProperty(description string) map[string]any {
	return map[string]any{"type": "boolean", "description": description}
}

func integerProperty(description string) map[string]any {
	return map[string]any{"type": "integer", "description": description, "minimum": 0}
}

func arrayProperty(description string, items map[string]any) map[string]any {
	return map[string]any{"type": "array", "description": description, "items": items}
}

func objectProperty(description string) map[string]any {
	return map[string]any{"type": "object", "description": description}
}
