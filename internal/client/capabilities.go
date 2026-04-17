package client

import (
	"os"
	"strings"

	"github.com/weill-labs/amux/internal/proto"
)

type envLookup func(string) (string, bool)

const (
	terminalUnknown = iota
	terminalKitty
	terminalGhostty
	terminalWezTerm
	terminalITerm2
)

// advertisedAttachCapabilities detects terminal support for the attach-time
// capability registry. This is intentionally conservative: if a feature cannot
// be inferred reliably from the client environment, we leave it disabled.
func advertisedAttachCapabilities() *proto.ClientCapabilities {
	caps := detectedAttachCapabilities(detectTerminalFlavor(os.LookupEnv))
	caps.BinaryPaneHistory = true
	caps.PredictionSupported = true
	if raw, ok := os.LookupEnv("AMUX_CLIENT_CAPABILITIES"); ok {
		caps = applyCapabilityOverride(caps, raw)
	}
	return &caps
}

func detectAttachCapabilitiesFromEnv(lookup envLookup) proto.ClientCapabilities {
	caps := detectedAttachCapabilities(detectTerminalFlavor(lookup))
	if raw, ok := lookup("AMUX_CLIENT_CAPABILITIES"); ok {
		caps = applyCapabilityOverride(caps, raw)
	}
	return caps
}

func detectedAttachCapabilities(flavor int) proto.ClientCapabilities {
	switch flavor {
	case terminalKitty:
		return proto.ClientCapabilities{
			KittyKeyboard:       true,
			Hyperlinks:          true,
			RichUnderline:       true,
			PromptMarkers:       true,
			GraphicsPlaceholder: true,
		}
	case terminalGhostty:
		return proto.ClientCapabilities{
			KittyKeyboard:       true,
			Hyperlinks:          true,
			RichUnderline:       true,
			CursorMetadata:      true,
			PromptMarkers:       true,
			GraphicsPlaceholder: true,
		}
	case terminalWezTerm:
		// WezTerm supports kitty keyboard protocol, but its docs describe it as
		// config-gated rather than on by default, so we do not advertise it here.
		return proto.ClientCapabilities{
			Hyperlinks:          true,
			RichUnderline:       true,
			CursorMetadata:      true,
			PromptMarkers:       true,
			GraphicsPlaceholder: true,
		}
	case terminalITerm2:
		return proto.ClientCapabilities{
			KittyKeyboard:  true,
			Hyperlinks:     true,
			CursorMetadata: true,
			PromptMarkers:  true,
		}
	default:
		return proto.ClientCapabilities{}
	}
}

func applyCapabilityOverride(base proto.ClientCapabilities, raw string) proto.ClientCapabilities {
	caps := base
	for _, token := range strings.Split(raw, ",") {
		token = strings.ToLower(strings.TrimSpace(token))
		if token == "" {
			continue
		}

		switch token {
		case "all":
			caps = proto.KnownClientCapabilities()
			continue
		case "legacy", "none":
			caps = proto.ClientCapabilities{}
			continue
		}

		enabled := true
		if strings.HasPrefix(token, "-") || strings.HasPrefix(token, "!") {
			enabled = false
			token = strings.TrimSpace(token[1:])
		}
		setCapability(&caps, token, enabled)
	}
	return caps
}

func setCapability(caps *proto.ClientCapabilities, name string, enabled bool) bool {
	switch name {
	case "kitty_keyboard":
		caps.KittyKeyboard = enabled
	case "hyperlinks":
		caps.Hyperlinks = enabled
	case "rich_underline":
		caps.RichUnderline = enabled
	case "cursor_metadata":
		caps.CursorMetadata = enabled
	case "prompt_markers":
		caps.PromptMarkers = enabled
	case "graphics_placeholder":
		caps.GraphicsPlaceholder = enabled
	case "binary_pane_history":
		caps.BinaryPaneHistory = enabled
	case "prediction_supported":
		caps.PredictionSupported = enabled
	default:
		return false
	}
	return true
}

func detectTerminalFlavor(lookup envLookup) int {
	termProgram := envValue(lookup, "TERM_PROGRAM")
	term := envValue(lookup, "TERM")

	switch {
	case envSet(lookup, "KITTY_WINDOW_ID") || strings.Contains(term, "kitty") || strings.EqualFold(termProgram, "kitty"):
		return terminalKitty
	case envSet(lookup, "GHOSTTY_RESOURCES_DIR") || strings.EqualFold(termProgram, "ghostty") || strings.Contains(term, "ghostty"):
		return terminalGhostty
	case envSet(lookup, "WEZTERM_EXECUTABLE") || envSet(lookup, "WEZTERM_PANE") || termProgram == "WezTerm":
		return terminalWezTerm
	case envSet(lookup, "ITERM_SESSION_ID") || termProgram == "iTerm.app":
		return terminalITerm2
	default:
		return terminalUnknown
	}
}

func envValue(lookup envLookup, key string) string {
	value, _ := lookup(key)
	return value
}

func envSet(lookup envLookup, key string) bool {
	value, ok := lookup(key)
	return ok && value != ""
}
