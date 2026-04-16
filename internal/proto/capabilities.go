package proto

import "strings"

// ClientCapabilities describes attach-time feature support for an amux client.
// These flags let the server gate modern terminal features explicitly instead
// of assuming every attached client supports the same escape semantics.
type ClientCapabilities struct {
	KittyKeyboard       bool `json:"kitty_keyboard,omitempty"`
	Hyperlinks          bool `json:"hyperlinks,omitempty"`
	RichUnderline       bool `json:"rich_underline,omitempty"`
	CursorMetadata      bool `json:"cursor_metadata,omitempty"`
	PromptMarkers       bool `json:"prompt_markers,omitempty"`
	GraphicsPlaceholder bool `json:"graphics_placeholder,omitempty"`
	BinaryPaneHistory   bool `json:"binary_pane_history,omitempty"`
}

// KnownClientCapabilities returns the capability set understood by this build.
// Negotiation intersects an attach's advertised set with this registry.
func KnownClientCapabilities() ClientCapabilities {
	return ClientCapabilities{
		KittyKeyboard:       true,
		Hyperlinks:          true,
		RichUnderline:       true,
		CursorMetadata:      true,
		PromptMarkers:       true,
		GraphicsPlaceholder: true,
		BinaryPaneHistory:   true,
	}
}

// NegotiateClientCapabilities returns the capability set enabled for an
// attached client. A nil advertisement means the client is using the legacy
// attach path and negotiates an empty capability set.
func NegotiateClientCapabilities(advertised *ClientCapabilities) ClientCapabilities {
	if advertised == nil {
		return ClientCapabilities{}
	}
	return advertised.Intersect(KnownClientCapabilities())
}

// Intersect keeps only capabilities enabled in both sets.
func (c ClientCapabilities) Intersect(other ClientCapabilities) ClientCapabilities {
	return ClientCapabilities{
		KittyKeyboard:       c.KittyKeyboard && other.KittyKeyboard,
		Hyperlinks:          c.Hyperlinks && other.Hyperlinks,
		RichUnderline:       c.RichUnderline && other.RichUnderline,
		CursorMetadata:      c.CursorMetadata && other.CursorMetadata,
		PromptMarkers:       c.PromptMarkers && other.PromptMarkers,
		GraphicsPlaceholder: c.GraphicsPlaceholder && other.GraphicsPlaceholder,
		BinaryPaneHistory:   c.BinaryPaneHistory && other.BinaryPaneHistory,
	}
}

// EnabledNames returns enabled capability names in stable display order.
func (c ClientCapabilities) EnabledNames() []string {
	names := make([]string, 0, 7)
	if c.KittyKeyboard {
		names = append(names, "kitty_keyboard")
	}
	if c.Hyperlinks {
		names = append(names, "hyperlinks")
	}
	if c.RichUnderline {
		names = append(names, "rich_underline")
	}
	if c.CursorMetadata {
		names = append(names, "cursor_metadata")
	}
	if c.PromptMarkers {
		names = append(names, "prompt_markers")
	}
	if c.GraphicsPlaceholder {
		names = append(names, "graphics_placeholder")
	}
	if c.BinaryPaneHistory {
		names = append(names, "binary_pane_history")
	}
	return names
}

// Summary returns a compact stable string for logs, tests, and `list-clients`.
func (c ClientCapabilities) Summary() string {
	names := c.EnabledNames()
	if len(names) == 0 {
		return "legacy"
	}
	return strings.Join(names, ",")
}
