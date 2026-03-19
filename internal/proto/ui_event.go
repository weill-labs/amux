package proto

// Client-local UI event names forwarded to the server.
const (
	UIEventDisplayPanesShown  = "display-panes-shown"
	UIEventDisplayPanesHidden = "display-panes-hidden"
	UIEventChooseTreeShown    = "choose-tree-shown"
	UIEventChooseTreeHidden   = "choose-tree-hidden"
	UIEventChooseWindowShown  = "choose-window-shown"
	UIEventChooseWindowHidden = "choose-window-hidden"
	UIEventCopyModeShown      = "copy-mode-shown"
	UIEventCopyModeHidden     = "copy-mode-hidden"
	UIEventInputBusy          = "input-busy"
	UIEventInputIdle          = "input-idle"
)

// IsKnownUIEvent reports whether name is a supported client UI event.
func IsKnownUIEvent(name string) bool {
	switch name {
	case UIEventDisplayPanesShown, UIEventDisplayPanesHidden,
		UIEventChooseTreeShown, UIEventChooseTreeHidden,
		UIEventChooseWindowShown, UIEventChooseWindowHidden,
		UIEventCopyModeShown, UIEventCopyModeHidden,
		UIEventInputBusy, UIEventInputIdle:
		return true
	default:
		return false
	}
}
