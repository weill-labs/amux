package copymode

import (
	"strconv"
	"strings"
)

// SearchBarText returns the search prompt to display in the status bar.
// Returns empty string when not actively searching.
func (cm *CopyMode) SearchBarText() string {
	var parts []string
	switch cm.prompt {
	case promptSearchForward:
		parts = append(parts, "/"+cm.promptBuf)
	case promptSearchBackward:
		parts = append(parts, "?"+cm.promptBuf)
	case promptGotoLine:
		parts = append(parts, ":"+cm.promptBuf)
	}
	if cm.pendingCount > 0 && cm.prompt == promptNone {
		parts = append(parts, strconv.Itoa(cm.pendingCount))
	}
	if cm.showPosition {
		parts = append(parts, "["+strconv.Itoa(cm.oy)+"/"+strconv.Itoa(cm.maxOY())+"]")
	}
	return strings.Join(parts, " ")
}
