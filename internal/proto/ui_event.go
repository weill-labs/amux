package proto

// Client-local UI event names forwarded to the server.
const (
	UIEventDisplayPanesShown   = "display-panes-shown"
	UIEventDisplayPanesHidden  = "display-panes-hidden"
	UIEventPrefixMessageShown  = "prefix-message-shown"
	UIEventPrefixMessageHidden = "prefix-message-hidden"
	UIEventChooseTreeShown     = "choose-tree-shown"
	UIEventChooseTreeHidden    = "choose-tree-hidden"
	UIEventChooseWindowShown   = "choose-window-shown"
	UIEventChooseWindowHidden  = "choose-window-hidden"
	UIEventCopyModeShown       = "copy-mode-shown"
	UIEventCopyModeHidden      = "copy-mode-hidden"
	UIEventInputBusy           = "input-busy"
	UIEventInputIdle           = "input-idle"
	UIEventClientFocusGained   = "client-focus-gained"
)

// IsKnownUIEvent reports whether name is a supported client UI event.
func IsKnownUIEvent(name string) bool {
	switch name {
	case UIEventDisplayPanesShown, UIEventDisplayPanesHidden,
		UIEventPrefixMessageShown, UIEventPrefixMessageHidden,
		UIEventChooseTreeShown, UIEventChooseTreeHidden,
		UIEventChooseWindowShown, UIEventChooseWindowHidden,
		UIEventCopyModeShown, UIEventCopyModeHidden,
		UIEventInputBusy, UIEventInputIdle,
		UIEventClientFocusGained:
		return true
	default:
		return false
	}
}
