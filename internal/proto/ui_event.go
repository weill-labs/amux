package proto

// Client-local UI event names forwarded to the server.
const (
	UIEventDisplayPanesShown  = "display-panes-shown"
	UIEventDisplayPanesHidden = "display-panes-hidden"
)

// IsKnownUIEvent reports whether name is a supported client UI event.
func IsKnownUIEvent(name string) bool {
	switch name {
	case UIEventDisplayPanesShown, UIEventDisplayPanesHidden:
		return true
	default:
		return false
	}
}
